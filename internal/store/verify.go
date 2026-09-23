package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CorruptError means a stored blob no longer hashes to its name.
type CorruptError struct {
	SHA256 string
	Got    string
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("blob %s is corrupt: content hashes to %s", e.SHA256, e.Got)
}

// Verify re-reads a blob and checks that it still hashes to its name. It
// returns *CorruptError on a mismatch and records the time of a good check.
func (s *Store) Verify(ctx context.Context, sum string) error {
	f, err := s.Open(sum)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, f}); err != nil {
		return fmt.Errorf("verify blob %s: %w", sum, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return &CorruptError{SHA256: sum, Got: got}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE blobs SET verified_at = ? WHERE sha256 = ?`, s.now().UTC().Unix(), sum)
	if err != nil {
		return fmt.Errorf("record verification of %s: %w", sum, err)
	}
	return nil
}

// Quarantine moves a blob out of blobs/ into quarantine/, so it is no longer
// served or counted as kept but is still there to inspect. The next pull
// downloads a fresh copy.
func (s *Store) Quarantine(sum string) (string, error) {
	if !s.Has(sum) {
		return "", fmt.Errorf("quarantine blob %s: %w", sum, ErrNotFound)
	}
	dir := filepath.Join(s.root, "quarantine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, sum+"-"+s.now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(s.Path(sum), dst); err != nil {
		return "", fmt.Errorf("quarantine blob %s: %w", sum, err)
	}
	return dst, nil
}
