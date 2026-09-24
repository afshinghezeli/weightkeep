package torrent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/testutil"
)

// keptRevision stores files and returns the manifest a pull would record.
func keptRevision(t *testing.T, files map[string][]byte) (*store.Store, *manifest.Manifest) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &manifest.Manifest{
		Version: manifest.FormatVersion, Repo: manifest.Repo{Type: "model", ID: "acme/tiny"},
		Commit: commit, FetchedAt: time.Now(),
	}
	for p, b := range files {
		blob, err := st.Put(ctx, bytes.NewReader(b), store.Expect{})
		if err != nil {
			t.Fatal(err)
		}
		m.Files = append(m.Files, manifest.File{Path: p, Size: blob.Size, SHA256: blob.SHA256, GitSHA1: blob.GitSHA1, LFS: len(b) > 1000})
	}
	return st, m
}

func TestSeedFromStoreToAnotherClient(t *testing.T) {
	files := sampleFiles(64 << 10)
	st, m := keptRevision(t, files)
	meta, err := ForRevision(st, m, Options{PieceLength: 64 << 10, V1Only: true})
	if err != nil {
		t.Fatal(err)
	}
	// Cached for next time.
	if _, err := os.Stat(CachePath(st.Root(), m.Repo, m.Commit)); err != nil {
		t.Errorf("torrent not cached: %v", err)
	}

	seeder, err := NewClient(st, ClientConfig{DataDir: t.TempDir(), NoDHT: true, NoWebSeeds: true})
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	if _, err := seeder.Seed(m, meta); err != nil {
		t.Fatal(err)
	}

	leecher, err := NewClient(st, ClientConfig{DataDir: t.TempDir(), NoDHT: true, NoWebSeeds: true})
	if err != nil {
		t.Fatal(err)
	}
	defer leecher.Close()
	out := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := leecher.Download(ctx, meta, out, seeder.Addr()); err != nil {
		t.Fatalf("download from the store-backed seeder: %v", err)
	}
	for p, want := range files {
		got, err := os.ReadFile(filepath.Join(out, commit, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: leecher got different bytes", p)
		}
	}
	// Seeding must never touch the blobs.
	for _, f := range m.Files {
		b, _ := os.ReadFile(st.Path(f.SHA256))
		if fmt.Sprintf("%x", sha256.Sum256(b)) != f.SHA256 {
			t.Errorf("%s: blob changed while seeding", f.Path)
		}
	}
}

func TestSeedRefusesPartialRevisions(t *testing.T) {
	st, m := keptRevision(t, map[string][]byte{"a.bin": bytes.Repeat([]byte("a"), 5000)})
	m.Files = append(m.Files, manifest.File{Path: "missing.bin", Size: 10, SHA256: strings.Repeat("0", 64), GitSHA1: strings.Repeat("0", 40)})
	if _, err := ForRevision(st, m, Options{}); err == nil {
		t.Fatal("built a torrent for a revision with missing files")
	}
}

func TestForRevisionCatchesCorruptBlobs(t *testing.T) {
	st, m := keptRevision(t, map[string][]byte{"a.bin": bytes.Repeat([]byte("a"), 5000)})
	p := st.Path(m.Files[0].SHA256)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, bytes.Repeat([]byte("b"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ForRevision(st, m, Options{}); err == nil || !strings.Contains(err.Error(), "weightkeep verify") {
		t.Fatalf("err = %v, want a pointer to verify", err)
	}
}

// TestLibtorrentLeechesFromStore has libtorrent, which checks pieces against
// the v2 merkle data, download from a store-backed seeder.
func TestLibtorrentLeechesFromStore(t *testing.T) {
	testutil.Network(t) // installs python-libtorrent with uv
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not installed")
	}
	files := sampleFiles(64 << 10)
	st, m := keptRevision(t, files)
	meta, err := ForRevision(st, m, Options{PieceLength: 64 << 10, V1Only: true})
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	seeder, err := NewClient(st, ClientConfig{DataDir: t.TempDir(), NoDHT: true, NoWebSeeds: true,
		Log: slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	defer func() {
		if t.Failed() {
			t.Log("seeder log:\n" + tail(logBuf.String(), 40))
		}
	}()
	if _, err := seeder.Seed(m, meta); err != nil {
		t.Fatal(err)
	}
	tf := filepath.Join(t.TempDir(), "r.torrent")
	if err := os.WriteFile(tf, meta, 0o644); err != nil {
		t.Fatal(err)
	}
	save := t.TempDir()
	port := seeder.Addr().(*net.TCPAddr).Port
	out, err := exec.Command(uv, "run", "--quiet", "--no-project", "--python", "3.13", "--with", "libtorrent==2.1.1",
		"python", "testdata/libtorrent_leech.py", tf, save, strconv.Itoa(port)).CombinedOutput()
	if err != nil {
		t.Fatalf("libtorrent: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "complete") {
		t.Fatalf("libtorrent: %s", out)
	}
	for p, want := range files {
		got, err := os.ReadFile(filepath.Join(save, commit, filepath.FromSlash(p)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: libtorrent got different bytes (%v)", p, err)
		}
	}
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
