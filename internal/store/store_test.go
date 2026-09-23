package store

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Reference values from `git hash-object` and `shasum -a 256`.
const (
	helloSHA256  = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	helloGitSHA1 = "ce013625030ba8dba906f756967f9e9ca394464a"
	emptySHA256  = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	emptyGitSHA1 = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func size(n int64) Expect { return Expect{Size: n, HasSize: true} }

func write(t *testing.T, p *Partial, s string) {
	t.Helper()
	if _, err := p.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, s *Store, content string) Blob {
	t.Helper()
	b, err := s.Put(context.Background(), strings.NewReader(content), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPut(t *testing.T) {
	tests := []struct {
		name    string
		content string
		exp     Expect
		want    Blob
	}{
		{"unknown size", "hello\n", Expect{}, Blob{SHA256: helloSHA256, GitSHA1: helloGitSHA1, Size: 6}},
		{"known size", "hello\n", size(6), Blob{SHA256: helloSHA256, GitSHA1: helloGitSHA1, Size: 6}},
		{"all expectations", "hello\n", Expect{SHA256: helloSHA256, GitSHA1: helloGitSHA1, Size: 6, HasSize: true}, Blob{SHA256: helloSHA256, GitSHA1: helloGitSHA1, Size: 6}},
		{"empty file", "", size(0), Blob{SHA256: emptySHA256, GitSHA1: emptyGitSHA1, Size: 0}},
		{"empty file, unknown size", "", Expect{}, Blob{SHA256: emptySHA256, GitSHA1: emptyGitSHA1, Size: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			got, err := s.Put(ctx, strings.NewReader(tt.content), tt.exp)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Put = %+v, want %+v", got, tt.want)
			}
			data, err := os.ReadFile(s.Path(tt.want.SHA256))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.content {
				t.Errorf("stored %q, want %q", data, tt.content)
			}
			if st, err := s.Stat(ctx, tt.want.SHA256); err != nil || st != tt.want {
				t.Errorf("Stat = %+v, %v", st, err)
			}
			if runtime.GOOS != "windows" {
				fi, _ := os.Stat(s.Path(tt.want.SHA256))
				if fi.Mode().Perm() != 0o444 {
					t.Errorf("mode = %v, want 0444", fi.Mode().Perm())
				}
			}
			assertNoTemp(t, s)
		})
	}
}

func TestPutMismatch(t *testing.T) {
	tests := []struct {
		name string
		exp  Expect
	}{
		{"sha256", Expect{SHA256: emptySHA256}},
		{"git sha1", Expect{GitSHA1: emptyGitSHA1}},
		{"too long", size(3)},
		{"too short", size(10)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := openTest(t)
			_, err := s.Put(context.Background(), strings.NewReader("hello\n"), tt.exp)
			if !errors.Is(err, ErrHashMismatch) && !errors.Is(err, ErrIncomplete) {
				t.Fatalf("err = %v, want a mismatch", err)
			}
			if n := countBlobs(t, dir); n != 0 {
				t.Errorf("%d blobs stored after a mismatch", n)
			}
			assertNoTemp(t, s)
		})
	}
}

func TestConcurrentPutOfSameContent(t *testing.T) {
	s, dir := openTest(t)
	content := bytes.Repeat([]byte("weights"), 100_000)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Put(context.Background(), bytes.NewReader(content), Expect{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if n := countBlobs(t, dir); n != 1 {
		t.Errorf("%d blobs, want 1", n)
	}
	assertNoTemp(t, s)
}

func TestResume(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	exp := Expect{SHA256: helloSHA256, Size: 6, HasSize: true}

	p, err := s.Partial(ctx, helloSHA256, exp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("hel")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Commit(ctx); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Commit on a short blob: err = %v, want ErrIncomplete", err)
	}
	p.Close() // connection dropped; keep the bytes

	p, err = s.Partial(ctx, helloSHA256, exp)
	if err != nil {
		t.Fatal(err)
	}
	if p.Offset() != 3 {
		t.Fatalf("Offset = %d, want 3", p.Offset())
	}
	if _, err := p.Write([]byte("lo\n")); err != nil {
		t.Fatal(err)
	}
	b, err := p.Commit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.GitSHA1 != helloGitSHA1 {
		t.Errorf("GitSHA1 after resume = %s, want %s", b.GitSHA1, helloGitSHA1)
	}
	assertNoTemp(t, s)
}

func TestResetAfterSourceIgnoredRange(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	p, err := s.Partial(ctx, helloSHA256, size(6))
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, "hel")
	if err := p.Reset(); err != nil {
		t.Fatal(err)
	}
	write(t, p, "hello\n")
	b, err := p.Commit(ctx)
	if err != nil || b.SHA256 != helloSHA256 || b.GitSHA1 != helloGitSHA1 {
		t.Fatalf("Commit = %+v, %v", b, err)
	}
}

func TestPartialIsExclusive(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	p, err := s.Partial(ctx, helloSHA256, Expect{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := s.Partial(ctx, helloSHA256, Expect{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Partial: err = %v, want ErrBusy", err)
	}
}

func TestInterruptedWriteLeavesNoBlob(t *testing.T) {
	s, dir := openTest(t)
	p, err := s.Partial(context.Background(), helloSHA256, Expect{})
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, "hel")
	p.Close() // as if the process died here
	if n := countBlobs(t, dir); n != 0 {
		t.Errorf("%d blobs after an interrupted write", n)
	}
	if s.Has(helloSHA256) {
		t.Error("Has reports an incomplete blob")
	}
}

func TestCancelledPut(t *testing.T) {
	s, dir := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, strings.NewReader("hello\n"), Expect{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := countBlobs(t, dir); n != 0 {
		t.Errorf("%d blobs after cancel", n)
	}
	assertNoTemp(t, s)
}

func TestVerifyDetectsCorruption(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	b, err := s.Put(ctx, strings.NewReader("hello\n"), Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, b.SHA256); err != nil {
		t.Fatalf("Verify on a good blob: %v", err)
	}

	path := s.Path(b.SHA256)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("jello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var corrupt *CorruptError
	if err := s.Verify(ctx, b.SHA256); !errors.As(err, &corrupt) {
		t.Fatalf("Verify = %v, want *CorruptError", err)
	}
	if corrupt.SHA256 != b.SHA256 || corrupt.Got == b.SHA256 {
		t.Errorf("CorruptError = %+v", corrupt)
	}

	moved, err := s.Quarantine(b.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if s.Has(b.SHA256) {
		t.Error("quarantined blob still counts as kept")
	}
	if data, err := os.ReadFile(moved); err != nil || string(data) != "jello\n" {
		t.Errorf("quarantined copy: %q, %v", data, err)
	}
}

func TestLookupGitSHA1(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.LookupGitSHA1(ctx, helloGitSHA1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before Put: err = %v, want ErrNotFound", err)
	}
	put(t, s, "hello\n")
	b, err := s.LookupGitSHA1(ctx, helloGitSHA1)
	if err != nil || b.SHA256 != helloSHA256 {
		t.Fatalf("LookupGitSHA1 = %+v, %v", b, err)
	}
}

func TestOpenRejectsBadNames(t *testing.T) {
	s, _ := openTest(t)
	for _, name := range []string{"", "../../etc/passwd", strings.Repeat("A", 64), helloSHA256[:63]} {
		if _, err := s.Open(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) = %v, want ErrNotFound", name, err)
		}
		if s.Has(name) {
			t.Errorf("Has(%q) = true", name)
		}
	}
	if _, err := s.Partial(context.Background(), "../escape", Expect{}); err == nil {
		t.Error("Partial accepted a path as key")
	}
}

func TestReopenAndNewerSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	put(t, s, "hello\n")
	s.Close()

	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if b, err := s.Stat(ctx, helloSHA256); err != nil || b.GitSHA1 != helloGitSHA1 {
		t.Fatalf("Stat after reopen = %+v, %v", b, err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := Open(ctx, dir); err == nil || !strings.Contains(err.Error(), "upgrade weightkeep") {
		t.Fatalf("opening a newer schema: err = %v", err)
	}
}

func countBlobs(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(dir, "blobs"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func assertNoTemp(t *testing.T, s *Store) {
	t.Helper()
	parts, _ := filepath.Glob(filepath.Join(s.Root(), "tmp", "*.part"))
	if len(parts) != 0 {
		t.Errorf("leftover partial files: %v", parts)
	}
}
