// Package store is weightkeep's content-addressed blob store.
//
// Blobs live under blobs/sha256/<2 hex>/<64 hex>, named by the SHA-256 of
// their content, read-only once written. Downloads go to tmp/ first and are
// renamed into place only after they are hashed and synced, so a file under
// blobs/ is always complete. Metadata lives in SQLite (weightkeep.db).
//
// This is the only package that writes under blobs/. See ADR 0003.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Blob describes a stored blob.
type Blob struct {
	SHA256  string // lowercase hex, the blob's identity
	GitSHA1 string // git blob SHA-1, empty if it was never computed
	Size    int64
}

// Errors returned by the store.
var (
	ErrNotFound     = errors.New("blob not found")
	ErrHashMismatch = errors.New("content does not match the expected hash")
	ErrBusy         = errors.New("another process is writing this blob")
)

var (
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	keyHex    = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)
)

// Store is an open blob store. It is safe for concurrent use.
type Store struct {
	root string
	db   *sql.DB
	now  func() time.Time
}

// Open opens (creating if needed) the store rooted at dir.
func Open(ctx context.Context, dir string) (*Store, error) {
	for _, d := range []string{dir, filepath.Join(dir, "blobs", "sha256"), filepath.Join(dir, "tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	db, err := openDB(ctx, filepath.Join(dir, "weightkeep.db"))
	if err != nil {
		return nil, err
	}
	return &Store{root: dir, db: db, now: time.Now}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Root returns the store's directory.
func (s *Store) Root() string { return s.root }

// DB returns the metadata database, for packages that keep their own tables
// in it (manifests, refs).
func (s *Store) DB() *sql.DB { return s.db }

// Path returns where the blob with the given SHA-256 lives, whether or not
// it exists.
func (s *Store) Path(sum string) string {
	return filepath.Join(s.root, "blobs", "sha256", sum[:2], sum)
}

// Has reports whether a complete blob exists.
func (s *Store) Has(sum string) bool {
	if !sha256Hex.MatchString(sum) {
		return false
	}
	_, err := os.Stat(s.Path(sum))
	return err == nil
}

// Open opens a blob for reading.
func (s *Store) Open(sum string) (*os.File, error) {
	if !sha256Hex.MatchString(sum) {
		return nil, fmt.Errorf("open blob %q: %w", sum, ErrNotFound)
	}
	f, err := os.Open(s.Path(sum))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("open blob %s: %w", sum, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("open blob %s: %w", sum, err)
	}
	return f, nil
}

// Stat returns what the store knows about a blob.
func (s *Store) Stat(ctx context.Context, sum string) (Blob, error) {
	if !s.Has(sum) {
		return Blob{}, fmt.Errorf("stat blob %s: %w", sum, ErrNotFound)
	}
	b := Blob{SHA256: sum}
	var gitSHA1 sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT git_sha1, size FROM blobs WHERE sha256 = ?`, sum).Scan(&gitSHA1, &b.Size)
	if errors.Is(err, sql.ErrNoRows) {
		// The file made it into place but the row didn't (crash in between).
		fi, statErr := os.Stat(s.Path(sum))
		if statErr != nil {
			return Blob{}, fmt.Errorf("stat blob %s: %w", sum, statErr)
		}
		b.Size = fi.Size()
		return b, nil
	}
	if err != nil {
		return Blob{}, fmt.Errorf("stat blob %s: %w", sum, err)
	}
	b.GitSHA1 = gitSHA1.String
	return b, nil
}

// LookupGitSHA1 finds the blob whose git blob SHA-1 is sha1.
func (s *Store) LookupGitSHA1(ctx context.Context, sha1 string) (Blob, error) {
	var b Blob
	err := s.db.QueryRowContext(ctx, `SELECT sha256, git_sha1, size FROM blobs WHERE git_sha1 = ? LIMIT 1`, sha1).
		Scan(&b.SHA256, &b.GitSHA1, &b.Size)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !s.Has(b.SHA256)) {
		return Blob{}, fmt.Errorf("git blob %s: %w", sha1, ErrNotFound)
	}
	if err != nil {
		return Blob{}, fmt.Errorf("git blob %s: %w", sha1, err)
	}
	return b, nil
}

func (s *Store) record(ctx context.Context, b Blob) error {
	var gitSHA1 any
	if b.GitSHA1 != "" {
		gitSHA1 = b.GitSHA1
	}
	now := s.now().UTC().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO blobs (sha256, git_sha1, size, created_at, verified_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (sha256) DO UPDATE SET
			git_sha1 = COALESCE(blobs.git_sha1, excluded.git_sha1),
			verified_at = excluded.verified_at`,
		b.SHA256, gitSHA1, b.Size, now, now)
	if err != nil {
		return fmt.Errorf("record blob %s: %w", b.SHA256, err)
	}
	return nil
}
