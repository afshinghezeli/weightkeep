package keep

import (
	"context"
	"errors"
	"fmt"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// VerifyResult is the outcome for one file of one revision.
type VerifyResult struct {
	Repo   manifest.Repo
	Commit string
	File   manifest.File
	// Err is nil for a good blob, *store.CorruptError for bitrot, or
	// another error if the blob couldn't be read.
	Err error
}

// Selector picks revisions. Empty fields match everything.
type Selector struct {
	Repo   manifest.Repo // ID "" matches every repo
	Commit string        // "" matches every commit of the repo
}

func (s Selector) matches(sum manifest.Summary) bool {
	if s.Repo.ID != "" && (sum.Repo.ID != s.Repo.ID || sum.Repo.Type != s.Repo.Type) {
		return false
	}
	return s.Commit == "" || s.Commit == sum.Commit
}

// Verify re-hashes the kept files of the selected revisions. A blob shared
// by several revisions is read once. Files that were never downloaded
// (left out by filters) are not reported.
func (k *Keeper) Verify(ctx context.Context, sel Selector, onResult func(VerifyResult)) error {
	sums, err := manifest.List(ctx, k.Store)
	if err != nil {
		return err
	}
	checked := map[string]error{}
	matched := false
	for _, sum := range sums {
		if !sel.matches(sum) {
			continue
		}
		matched = true
		m, err := manifest.Load(ctx, k.Store, sum.Repo, sum.Commit)
		if err != nil {
			return err
		}
		for _, f := range m.Files {
			verr, done := checked[f.SHA256]
			// Report every path that shares a checked blob, even if the
			// caller quarantined it after the first report.
			if !done && (f.SHA256 == "" || !k.Store.Has(f.SHA256)) {
				continue
			}
			if !done {
				verr = k.Store.Verify(ctx, f.SHA256)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				checked[f.SHA256] = verr
			}
			onResult(VerifyResult{Repo: sum.Repo, Commit: sum.Commit, File: f, Err: verr})
		}
	}
	if !matched {
		what := "anything"
		if sel.Repo.ID != "" {
			what = sel.Repo.String()
			if sel.Commit != "" {
				what += "@" + sel.Commit
			}
		}
		return fmt.Errorf("not keeping %s: %w", what, manifest.ErrNotFound)
	}
	return nil
}

// IsCorrupt reports whether a VerifyResult error means bitrot.
func IsCorrupt(err error) bool {
	var c *store.CorruptError
	return errors.As(err, &c)
}
