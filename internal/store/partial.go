package store

import (
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: git blob ids are SHA-1; we compute them, we don't trust them for security
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/gofrs/flock"
)

// ErrIncomplete means Commit was called before all expected bytes arrived.
// The partial file is kept so the download can resume.
var ErrIncomplete = errors.New("blob is incomplete")

// maxRehashForGitSHA1 bounds the second read needed to compute a git blob
// SHA-1 when the size wasn't known up front. Regular (non-LFS) files on the
// Hub are small; LFS files are identified by SHA-256 and don't need it.
const maxRehashForGitSHA1 = 64 << 20

// Expect is what the caller knows about a blob before it arrives. Empty
// hashes and HasSize=false mean "don't check".
type Expect struct {
	SHA256  string
	GitSHA1 string
	Size    int64
	HasSize bool
}

// Partial is a blob being written, possibly resumed from an earlier attempt.
// It holds a file lock, so two processes can't write the same partial.
type Partial struct {
	s      *Store
	path   string
	f      *os.File
	lock   *flock.Flock
	exp    Expect
	sha256 hash.Hash
	sha1   hash.Hash // nil when the size isn't known up front
	n      int64
	done   bool
}

// Partial opens tmp/<key>.part for writing, resuming from whatever an
// earlier attempt left there. key must be 40 or 64 lowercase hex characters
// (normally the expected SHA-256 or git SHA-1).
//
// If another process holds the partial, Partial returns ErrBusy.
func (s *Store) Partial(ctx context.Context, key string, exp Expect) (*Partial, error) {
	if !keyHex.MatchString(key) {
		return nil, fmt.Errorf("partial key %q: must be 40 or 64 lowercase hex characters", key)
	}
	if exp.SHA256 != "" && !sha256Hex.MatchString(exp.SHA256) {
		return nil, fmt.Errorf("expected sha256 %q is not 64 lowercase hex characters", exp.SHA256)
	}
	base := filepath.Join(s.root, "tmp", key)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock := flock.New(base + ".lock")
	// One attempt. (TryLockContext retries until the context ends, which
	// would turn "busy" into "hang".)
	ok, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", key, err)
	}
	if !ok {
		return nil, fmt.Errorf("partial %s: %w", key, ErrBusy)
	}

	p := &Partial{s: s, path: base + ".part", lock: lock, exp: exp, sha256: sha256.New()}
	p.f, err = os.OpenFile(p.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("open partial %s: %w", key, err)
	}
	if err := p.resume(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// resume re-hashes whatever is already in the file, so the final hashes
// cover the whole content.
func (p *Partial) resume() error {
	fi, err := p.f.Stat()
	if err != nil {
		return err
	}
	if p.exp.HasSize && fi.Size() > p.exp.Size {
		// Longer than the blob can be: leftover from something else.
		if err := p.f.Truncate(0); err != nil {
			return err
		}
	}
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	p.resetHashes()
	n, err := io.Copy(p.hashWriter(), p.f)
	if err != nil {
		return fmt.Errorf("read partial: %w", err)
	}
	p.n = n
	return nil
}

func (p *Partial) resetHashes() {
	p.sha256.Reset()
	p.sha1 = nil
	if p.exp.HasSize {
		p.sha1 = sha1.New() //nolint:gosec // G401: git blob id, see import
		_, _ = p.sha1.Write([]byte("blob " + strconv.FormatInt(p.exp.Size, 10) + "\x00"))
	}
}

func (p *Partial) hashWriter() io.Writer {
	if p.sha1 != nil {
		return io.MultiWriter(p.sha256, p.sha1)
	}
	return p.sha256
}

// Offset is the number of bytes already written. A resumed download should
// ask its source for bytes from Offset onwards.
func (p *Partial) Offset() int64 { return p.n }

// Write appends to the blob.
func (p *Partial) Write(b []byte) (int, error) {
	if p.exp.HasSize && p.n+int64(len(b)) > p.exp.Size {
		return 0, fmt.Errorf("write past expected size %d: %w", p.exp.Size, ErrHashMismatch)
	}
	n, err := p.f.Write(b)
	_, _ = p.hashWriter().Write(b[:n]) // hashes never return errors
	p.n += int64(n)
	return n, err
}

// Reset discards what has been written so far, for a source that ignored a
// Range request and started from byte zero.
func (p *Partial) Reset() error {
	if err := p.f.Truncate(0); err != nil {
		return err
	}
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	p.resetHashes()
	p.n = 0
	return nil
}

// Commit verifies the content against the expectation and moves it into the
// store. On a hash mismatch the partial is deleted, since resuming it would
// only reproduce the bad bytes. On ErrIncomplete it is kept.
func (p *Partial) Commit(ctx context.Context) (Blob, error) {
	if p.done {
		return Blob{}, errors.New("partial already closed")
	}
	if p.exp.HasSize && p.n < p.exp.Size {
		return Blob{}, fmt.Errorf("have %d of %d bytes: %w", p.n, p.exp.Size, ErrIncomplete)
	}
	b := Blob{SHA256: hex.EncodeToString(p.sha256.Sum(nil)), Size: p.n}
	if p.sha1 != nil {
		b.GitSHA1 = hex.EncodeToString(p.sha1.Sum(nil))
	} else if p.n <= maxRehashForGitSHA1 {
		sum, err := gitBlobSHA1(p.f, p.n)
		if err != nil {
			return Blob{}, err
		}
		b.GitSHA1 = sum
	}

	if err := p.check(b); err != nil {
		p.Abort()
		return Blob{}, err
	}

	if err := p.f.Sync(); err != nil {
		return Blob{}, fmt.Errorf("sync %s: %w", p.path, err)
	}
	if err := p.f.Close(); err != nil {
		return Blob{}, fmt.Errorf("close %s: %w", p.path, err)
	}
	if err := os.Chmod(p.path, 0o444); err != nil {
		return Blob{}, err
	}
	dst := p.s.Path(b.SHA256)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Blob{}, err
	}
	if err := os.Rename(p.path, dst); err != nil {
		// Someone else stored the same content first (on Windows, renaming
		// over a read-only file fails). Theirs is as good as ours.
		if !p.s.Has(b.SHA256) {
			return Blob{}, fmt.Errorf("move blob into place: %w", err)
		}
		os.Remove(p.path)
	}
	syncDir(filepath.Dir(dst))
	p.done = true
	_ = p.lock.Unlock()

	if err := p.s.record(ctx, b); err != nil {
		return Blob{}, err
	}
	return b, nil
}

func (p *Partial) check(b Blob) error {
	if p.exp.SHA256 != "" && b.SHA256 != p.exp.SHA256 {
		return fmt.Errorf("sha256 is %s, expected %s: %w", b.SHA256, p.exp.SHA256, ErrHashMismatch)
	}
	if p.exp.GitSHA1 != "" && b.GitSHA1 != p.exp.GitSHA1 {
		return fmt.Errorf("git blob sha1 is %s, expected %s: %w", b.GitSHA1, p.exp.GitSHA1, ErrHashMismatch)
	}
	return nil
}

// Close releases the partial but keeps its bytes for a later resume.
func (p *Partial) Close() error {
	if p.done {
		return nil
	}
	p.done = true
	err := p.f.Close()
	_ = p.lock.Unlock()
	return err
}

// Abort releases the partial and deletes its bytes.
func (p *Partial) Abort() {
	if !p.done {
		p.f.Close()
		p.done = true
	}
	os.Remove(p.path)
	_ = p.lock.Unlock()
}

// Put stores everything read from r. It is a convenience for callers that
// don't need resume.
func (s *Store) Put(ctx context.Context, r io.Reader, exp Expect) (Blob, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return Blob{}, err
	}
	p, err := s.Partial(ctx, hex.EncodeToString(key[:]), exp)
	if err != nil {
		return Blob{}, err
	}
	if _, err := io.Copy(p, ctxReader{ctx, r}); err != nil {
		p.Abort()
		return Blob{}, fmt.Errorf("write blob: %w", err)
	}
	b, err := p.Commit(ctx)
	if err != nil && !errors.Is(err, ErrHashMismatch) {
		p.Abort()
	}
	return b, err
}

func gitBlobSHA1(f *os.File, size int64) (string, error) {
	h := sha1.New() //nolint:gosec // G401: git blob id, see import
	_, _ = h.Write([]byte("blob " + strconv.FormatInt(size, 10) + "\x00"))
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, size)); err != nil {
		return "", fmt.Errorf("hash git blob: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// syncDir makes a rename durable. Directories can't be synced on Windows,
// where NTFS journals the rename anyway.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync() // best effort
		d.Close()
	}
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
