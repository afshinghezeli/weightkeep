package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// BlobInfo is a blob on disk.
type BlobInfo struct {
	SHA256  string
	Size    int64
	ModTime time.Time
}

// Blobs lists every complete blob on disk.
func (s *Store) Blobs(ctx context.Context) ([]BlobInfo, error) {
	var out []BlobInfo
	root := filepath.Join(s.root, "blobs", "sha256")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !sha256Hex.MatchString(d.Name()) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, BlobInfo{SHA256: d.Name(), Size: fi.Size(), ModTime: fi.ModTime()})
		return nil
	})
	return out, err
}

// Remove deletes a blob and its metadata.
func (s *Store) Remove(ctx context.Context, sum string) error {
	if !sha256Hex.MatchString(sum) {
		return fmt.Errorf("remove blob %q: %w", sum, ErrNotFound)
	}
	p := s.Path(sum)
	_ = os.Chmod(p, 0o644) // Windows refuses to delete read-only files
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove blob %s: %w", sum, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blobs WHERE sha256 = ?`, sum); err != nil {
		return fmt.Errorf("remove blob %s: %w", sum, err)
	}
	return nil
}

// TmpCleanup reports what CleanTmp removed (or would remove).
type TmpCleanup struct {
	Locks    int
	Partials int
	Bytes    int64 // in removed partials
}

// CleanTmp removes lock files older than lockAge and partial downloads
// older than partialAge from tmp/. Anything another process currently holds
// is left alone.
func (s *Store) CleanTmp(now time.Time, lockAge, partialAge time.Duration, dryRun bool) (TmpCleanup, error) {
	var res TmpCleanup
	dir := filepath.Join(s.root, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}
	for _, e := range entries {
		name := e.Name()
		key, ext, _ := strings.Cut(name, ".")
		if !keyHex.MatchString(key) || (ext != "lock" && ext != "part") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		age := now.Sub(fi.ModTime())
		if (ext == "lock" && age < lockAge) || (ext == "part" && age < partialAge) {
			continue
		}
		// Only touch what nobody holds.
		lock := flock.New(filepath.Join(dir, key+".lock"))
		ok, err := lock.TryLock()
		if err != nil || !ok {
			continue
		}
		if ext == "part" {
			if !dryRun {
				err = os.Remove(filepath.Join(dir, name))
			}
			if err == nil {
				res.Partials++
				res.Bytes += fi.Size()
			}
		}
		_ = lock.Unlock()
		if ext == "lock" {
			if !dryRun {
				err = os.Remove(filepath.Join(dir, name))
			}
			if err == nil {
				res.Locks++
			}
		}
	}
	return res, nil
}
