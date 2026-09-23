// Package fetch downloads repo files into the store, resuming interrupted
// transfers and verifying every file against the hashes the Hub's tree
// listing gave for it.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// Target is one file to fetch, described by the tree listing.
type Target struct {
	Repo   hub.Repo
	Commit string
	Path   string
	Size   int64
	// For LFS files SHA256 is set and TreeOID is the pointer's id. For
	// regular files TreeOID is the content's git blob id and SHA256 is empty.
	SHA256  string
	TreeOID string
	LFS     bool
}

// TargetFromTree converts a tree entry.
func TargetFromTree(repo hub.Repo, commit string, e hub.TreeEntry) Target {
	t := Target{Repo: repo, Commit: commit, Path: e.Path, Size: e.Size, TreeOID: e.OID}
	if e.LFS != nil {
		t.LFS, t.SHA256, t.Size = true, e.LFS.OID, e.LFS.Size
	}
	return t
}

func (t Target) key() string {
	if t.LFS {
		return t.SHA256
	}
	return t.TreeOID
}

func (t Target) expect() store.Expect {
	e := store.Expect{SHA256: t.SHA256, Size: t.Size, HasSize: true}
	if !t.LFS {
		e.GitSHA1 = t.TreeOID
	}
	return e
}

// State is what an Event reports.
type State int

const (
	Started State = iota
	Progress
	Done
	Skipped // already in the store
	Retrying
	Failed
)

// Event is a progress report for one file.
type Event struct {
	Path  string
	State State
	Done  int64 // bytes of this file present so far
	Total int64
	Err   error
}

// Fetcher downloads files into a store.
type Fetcher struct {
	Store *store.Store
	Hub   *hub.Client
	// Parallel is how many files download at once. Default 4.
	Parallel int
	// Retries per file for transient failures. Default 6.
	Retries int
	// Backoff before the first retry, doubling each time. Default 1s.
	Backoff time.Duration
	// Events, if set, receives progress. It is called from several
	// goroutines and must not block for long.
	Events func(Event)
}

func (f *Fetcher) emit(e Event) {
	if f.Events != nil {
		f.Events(e)
	}
}

// Have reports whether the target's content is already in the store, and
// returns its blob if so.
func (f *Fetcher) Have(ctx context.Context, t Target) (store.Blob, bool) {
	if t.LFS {
		b, err := f.Store.Stat(ctx, t.SHA256)
		return b, err == nil
	}
	b, err := f.Store.LookupGitSHA1(ctx, t.TreeOID)
	return b, err == nil
}

// File fetches one file, or returns the stored blob if it is already there.
func (f *Fetcher) File(ctx context.Context, t Target) (store.Blob, error) {
	if b, ok := f.Have(ctx, t); ok {
		f.emit(Event{Path: t.Path, State: Skipped, Done: t.Size, Total: t.Size})
		return b, nil
	}
	b, err := f.fetch(ctx, t)
	if err != nil {
		f.emit(Event{Path: t.Path, State: Failed, Err: err})
		return store.Blob{}, fmt.Errorf("%s: %w", t.Path, err)
	}
	f.emit(Event{Path: t.Path, State: Done, Done: t.Size, Total: t.Size})
	return b, nil
}

func (f *Fetcher) fetch(ctx context.Context, t Target) (store.Blob, error) {
	p, err := f.openPartial(ctx, t)
	if err != nil {
		return store.Blob{}, err
	}
	if p == nil { // someone else finished it while we waited
		b, _ := f.Have(ctx, t)
		return b, nil
	}
	defer p.Close()
	f.emit(Event{Path: t.Path, State: Started, Done: p.Offset(), Total: t.Size})

	retries, backoff := f.Retries, f.Backoff
	if retries == 0 {
		retries = 6
	}
	if backoff == 0 {
		backoff = time.Second
	}
	for attempt := 0; ; attempt++ {
		err := f.transfer(ctx, t, p)
		if err == nil {
			return p.Commit(ctx)
		}
		if ctx.Err() != nil {
			return store.Blob{}, ctx.Err()
		}
		if !hub.Retryable(err) || attempt >= retries {
			return store.Blob{}, err
		}
		f.emit(Event{Path: t.Path, State: Retrying, Done: p.Offset(), Total: t.Size, Err: err})
		select {
		case <-ctx.Done():
			return store.Blob{}, ctx.Err()
		case <-time.After(backoff << min(attempt, 6)):
		}
	}
}

// openPartial opens the store partial for t, waiting while another process
// holds it. It returns nil if the blob appeared in the meantime.
func (f *Fetcher) openPartial(ctx context.Context, t Target) (*store.Partial, error) {
	for {
		p, err := f.Store.Partial(ctx, t.key(), t.expect())
		if !errors.Is(err, store.ErrBusy) {
			return p, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if _, ok := f.Have(ctx, t); ok {
			return nil, nil
		}
	}
}

// transfer copies the rest of the file into p. A nil error means p holds
// Size bytes.
func (f *Fetcher) transfer(ctx context.Context, t Target, p *store.Partial) error {
	if p.Offset() == t.Size {
		return nil
	}
	body, err := f.Hub.Download(ctx, t.Repo, t.Commit, t.Path, p.Offset())
	if err != nil {
		return err
	}
	defer body.Close()
	if body.Offset != p.Offset() {
		// The server ignored our Range and started from the beginning.
		if err := p.Reset(); err != nil {
			return err
		}
	}
	w := &progressWriter{w: p, f: f, path: t.Path, done: p.Offset(), total: t.Size}
	if _, err := io.Copy(w, body); err != nil {
		if errors.Is(err, store.ErrHashMismatch) {
			return err // longer than the tree said: not retryable
		}
		return &transientError{err}
	}
	if p.Offset() < t.Size {
		return &transientError{fmt.Errorf("connection closed after %d of %d bytes", p.Offset(), t.Size)}
	}
	return nil
}

// transientError marks a read failure mid-body as retryable. hub.Retryable
// treats errors that aren't HTTP answers as network failures.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

type progressWriter struct {
	w           io.Writer
	f           *Fetcher
	path        string
	done, total int64
	last        time.Time
}

func (pw *progressWriter) Write(b []byte) (int, error) {
	n, err := pw.w.Write(b)
	pw.done += int64(n)
	if now := time.Now(); now.Sub(pw.last) > 100*time.Millisecond {
		pw.last = now
		pw.f.emit(Event{Path: pw.path, State: Progress, Done: pw.done, Total: pw.total})
	}
	return n, err
}

// Result is the outcome for one target of Files.
type Result struct {
	Target Target
	Blob   store.Blob
	Err    error
}

// Files fetches targets concurrently. It doesn't stop at the first failure:
// every result comes back, so the caller can report all of them.
func (f *Fetcher) Files(ctx context.Context, targets []Target) []Result {
	n := f.Parallel
	if n <= 0 {
		n = 4
	}
	results := make([]Result, len(targets))
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = Result{Target: t, Err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			b, err := f.File(ctx, t)
			results[i] = Result{Target: t, Blob: b, Err: err}
		}()
	}
	wg.Wait()
	return results
}
