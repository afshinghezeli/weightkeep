package hfcache

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

type fixture struct {
	st *store.Store
	m  *manifest.Manifest
}

// setup keeps a revision with a regular file, a nested LFS file and an LFS
// file that was left out by a filter.
func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	put := func(content string) store.Blob {
		b, err := st.Put(ctx, bytes.NewReader([]byte(content)), store.Expect{})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cfg := put(`{"n":1}`)
	w := put("weights weights weights")
	m := &manifest.Manifest{
		Version:   manifest.FormatVersion,
		Repo:      manifest.Repo{Type: "model", ID: "acme/tiny"},
		Commit:    "0123456789abcdef0123456789abcdef01234567",
		FetchedAt: time.Now(),
		Files: []manifest.File{
			{Path: "config.json", Size: cfg.Size, SHA256: cfg.SHA256, GitSHA1: cfg.GitSHA1},
			{Path: "sub/dir/model.safetensors", Size: w.Size, SHA256: w.SHA256, GitSHA1: "1111111111111111111111111111111111111111", LFS: true},
			{Path: "big.gguf", Size: 99, SHA256: "2222222222222222222222222222222222222222222222222222222222222222", GitSHA1: "3333333333333333333333333333333333333333", LFS: true},
		},
	}
	return fixture{st, m}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestToCache(t *testing.T) {
	f := setup(t)
	cache := t.TempDir()
	res, err := ToCache(context.Background(), f.st, f.m, cache, []string{"main"}, Auto)
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(cache, "models--acme--tiny")
	if res.Dir != repoDir || res.Written != 2 || len(res.Missing) != 1 || res.Missing[0] != "big.gguf" {
		t.Errorf("result = %+v", res)
	}

	cfg := f.m.Files[0]
	if got := read(t, filepath.Join(repoDir, "blobs", cfg.GitSHA1)); got != `{"n":1}` {
		t.Errorf("regular file blob named by git sha1: %q", got)
	}
	w := f.m.Files[1]
	if got := read(t, filepath.Join(repoDir, "blobs", w.SHA256)); got != "weights weights weights" {
		t.Errorf("LFS blob named by sha256: %q", got)
	}

	snap := filepath.Join(repoDir, "snapshots", f.m.Commit)
	if got := read(t, filepath.Join(snap, "sub", "dir", "model.safetensors")); got != "weights weights weights" {
		t.Errorf("nested snapshot file: %q", got)
	}
	if runtime.GOOS != "windows" {
		target, err := os.Readlink(filepath.Join(snap, "sub", "dir", "model.safetensors"))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join("..", "..", "..", "..", "blobs", w.SHA256); target != want {
			t.Errorf("snapshot link = %q, want relative %q", target, want)
		}
	}
	if got := read(t, filepath.Join(repoDir, "refs", "main")); got != f.m.Commit {
		t.Errorf("refs/main = %q", got)
	}

	// Exporting again changes nothing.
	res, err = ToCache(context.Background(), f.st, f.m, cache, []string{"main"}, Auto)
	if err != nil || res.Written != 0 || res.Existing != 2 {
		t.Errorf("second export = %+v, %v", res, err)
	}
}

func TestToCacheLeavesForeignBlobsAlone(t *testing.T) {
	f := setup(t)
	cache := t.TempDir()
	cfg := f.m.Files[0]
	// huggingface_hub already downloaded this blob, but with other content
	// of a different size: refuse rather than overwrite.
	blob := filepath.Join(cache, "models--acme--tiny", "blobs", cfg.GitSHA1)
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("something else entirely"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ToCache(context.Background(), f.st, f.m, cache, nil, Auto); err == nil {
		t.Fatal("expected an error for a conflicting existing blob")
	}
	if got := read(t, blob); got != "something else entirely" {
		t.Error("existing blob was overwritten")
	}
}

func TestLinkModes(t *testing.T) {
	for _, mode := range []LinkMode{Hardlink, Symlink, Copy} {
		t.Run(string(mode), func(t *testing.T) {
			if mode == Symlink && runtime.GOOS == "windows" {
				t.Skip("symlinks need privileges on Windows")
			}
			f := setup(t)
			dir := t.TempDir()
			res, err := ToDir(context.Background(), f.st, f.m, dir, mode)
			if err != nil {
				t.Fatal(err)
			}
			if res.Methods[mode] != 2 {
				t.Errorf("methods = %v", res.Methods)
			}
			if got := read(t, filepath.Join(dir, "sub", "dir", "model.safetensors")); got != "weights weights weights" {
				t.Errorf("content = %q", got)
			}
		})
	}
}

func TestBadRefName(t *testing.T) {
	f := setup(t)
	if _, err := ToCache(context.Background(), f.st, f.m, t.TempDir(), []string{"../../escape"}, Auto); err == nil {
		t.Error("ref name with .. accepted")
	}
}
