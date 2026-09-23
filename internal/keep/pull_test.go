package keep

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

var (
	cfg     = fakehub.File{Path: "config.json", Content: []byte(`{"n": 1}`)}
	lic     = fakehub.File{Path: "LICENSE", Content: []byte("Apache License 2.0 ...")}
	q4      = fakehub.File{Path: "model-Q4_K_M.gguf", Content: bytes.Repeat([]byte("4"), 40_000), LFS: true}
	q8      = fakehub.File{Path: "model-Q8_0.gguf", Content: bytes.Repeat([]byte("8"), 80_000), LFS: true}
	shard   = fakehub.File{Path: "onnx/model.onnx", Content: bytes.Repeat([]byte("o"), 10_000), LFS: true}
	// Same bytes as q4 under another name, as bartowski's Q4_K_M/Q4_K_L.
	q4dup = fakehub.File{Path: "model-Q4_K_L.gguf", Content: q4.Content, LFS: true}
	tinyRep = hub.Repo{Type: hub.Model, ID: "acme/tiny"}
)

func newKeeper(t *testing.T) (*Keeper, *fakehub.Hub) {
	t.Helper()
	h := fakehub.New(&fakehub.Repo{ID: tinyRep.ID, License: "apache-2.0", Files: []fakehub.File{cfg, lic, q4, q4dup, q8, shard}})
	t.Cleanup(h.Close)
	client, err := hub.New(h.URL, hub.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Keeper{
		Store:   st,
		Hub:     client,
		Fetcher: &fetch.Fetcher{Store: st, Hub: client, Backoff: time.Millisecond},
		Now:     func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) },
	}, h
}

func TestPullEverything(t *testing.T) {
	k, h := newKeeper(t)
	ctx := context.Background()
	res, err := k.Pull(ctx, PullRequest{Repo: tinyRep})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Downloaded)+len(res.Present) != 6 || len(res.Skipped) != 0 {
		t.Errorf("downloaded %v, skipped %v", res.Downloaded, res.Skipped)
	}
	m := res.Manifest
	for _, f := range m.Files {
		if f.SHA256 == "" || !k.Store.Has(f.SHA256) {
			t.Errorf("%s: sha256 %q not in store", f.Path, f.SHA256)
		}
	}
	if f, _ := m.File("config.json"); f.SHA256 != cfg.SHA256() || f.GitSHA1 != cfg.GitSHA1() || f.LFS {
		t.Errorf("config.json entry = %+v", f)
	}
	if got := m.License.IDs; len(got) != 1 || got[0] != "apache-2.0" {
		t.Errorf("license = %v", got)
	}

	// The manifest on disk matches, and main resolves to the commit.
	saved, err := manifest.Load(ctx, k.Store, manifest.Repo{Type: "model", ID: tinyRep.ID}, m.Commit)
	if err != nil || len(saved.Files) != 6 {
		t.Fatalf("saved manifest: %v, %v", saved, err)
	}
	if c, _, err := manifest.ResolveRef(ctx, k.Store, manifest.Repo{Type: "model", ID: tinyRep.ID}, "main"); err != nil || c != m.Commit {
		t.Errorf("ref main = %s, %v", c, err)
	}

	// Second run: only the revision request.
	h.ResetRequests()
	res, err = k.Pull(ctx, PullRequest{Repo: tinyRep})
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.Requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Path, "/revision/main") {
		t.Errorf("second pull made requests %+v, want just the revision lookup", reqs)
	}
	if len(res.Downloaded) != 0 || len(res.Present) != 6 {
		t.Errorf("second pull: downloaded %v present %v", res.Downloaded, res.Present)
	}

	// Pinned to a kept commit: no requests at all.
	h.ResetRequests()
	res, err = k.Pull(ctx, PullRequest{Repo: tinyRep, Revision: m.Commit})
	if err != nil || !res.Offline {
		t.Fatalf("pinned pull: %+v, %v", res, err)
	}
	if n := len(h.Requests()); n != 0 {
		t.Errorf("pinned pull of a kept commit made %d requests", n)
	}
}

func TestPullWithFilters(t *testing.T) {
	k, _ := newKeeper(t)
	res, err := k.Pull(context.Background(), PullRequest{Repo: tinyRep, Include: []string{"*Q4_K_M*"}})
	if err != nil {
		t.Fatal(err)
	}
	has := func(list []string, p string) bool {
		for _, x := range list {
			if x == p {
				return true
			}
		}
		return false
	}
	for _, p := range []string{"config.json", "LICENSE", "model-Q4_K_M.gguf"} {
		if !has(res.Downloaded, p) {
			t.Errorf("%s not downloaded; small files and the included quant must be", p)
		}
	}
	for _, p := range []string{"model-Q8_0.gguf", "onnx/model.onnx", "model-Q4_K_L.gguf"} {
		if !has(res.Skipped, p) {
			t.Errorf("%s should have been skipped", p)
		}
	}
	// The manifest still describes the whole revision.
	if len(res.Manifest.Files) != 6 {
		t.Errorf("manifest lists %d files, want all 6", len(res.Manifest.Files))
	}

	// A later pull with another filter adds to what is kept.
	res, err = k.Pull(context.Background(), PullRequest{Repo: tinyRep, Exclude: []string{"*.gguf"}})
	if err != nil {
		t.Fatal(err)
	}
	if !has(res.Downloaded, "onnx/model.onnx") || has(res.Downloaded, "model-Q8_0.gguf") {
		t.Errorf("second filtered pull downloaded %v", res.Downloaded)
	}
}

func TestPullErrors(t *testing.T) {
	k, h := newKeeper(t)
	ctx := context.Background()

	if _, err := k.Pull(ctx, PullRequest{Repo: hub.Repo{Type: hub.Model, ID: "acme/nope"}}); !errors.Is(err, hub.ErrRepoNotFound) {
		t.Errorf("missing repo: %v", err)
	}
	if _, err := k.Pull(ctx, PullRequest{Repo: tinyRep, Revision: "no-such-branch"}); !errors.Is(err, hub.ErrRevisionNotFound) {
		t.Errorf("bad revision: %v", err)
	}
	if _, err := k.Pull(ctx, PullRequest{Repo: tinyRep, Include: []string{"[oops"}}); err == nil {
		t.Error("bad glob accepted")
	}

	// One file keeps failing: the others still land, the error names it,
	// and no manifest is recorded for a half-kept revision.
	h.Fail("cdn:/xet-bridge/"+q8.SHA256(), http.StatusNotFound, 100)
	res, err := k.Pull(ctx, PullRequest{Repo: tinyRep})
	var fe *FileError
	if !errors.As(err, &fe) || len(fe.Failures) != 1 || fe.Failures[0].Target.Path != q8.Path {
		t.Fatalf("err = %v, want a FileError for %s", err, q8.Path)
	}
	if len(res.Downloaded)+len(res.Present) != 5 {
		t.Errorf("downloaded %v present %v, want the other 5 files", res.Downloaded, res.Present)
	}
	if _, err := manifest.Load(ctx, k.Store, manifest.Repo{Type: "model", ID: tinyRep.ID}, res.Manifest.Commit); !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("manifest saved for an incomplete pull: %v", err)
	}
}

func TestPullCanonicalisesLegacyID(t *testing.T) {
	k, h := newKeeper(t)
	h.Alias("tiny", "acme/tiny")
	res, err := k.Pull(context.Background(), PullRequest{Repo: hub.Repo{Type: hub.Model, ID: "tiny"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.Repo.ID != "acme/tiny" {
		t.Errorf("manifest repo = %q, want the canonical id", res.Manifest.Repo.ID)
	}
}

func TestGlobs(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		{"*.gguf", []string{"a.gguf", "sub/dir/a.gguf"}, []string{"a.ggufx", "a.bin"}},
		{"*Q4_K_M*", []string{"m-Q4_K_M.gguf", "x/m-Q4_K_M-00001-of-2.gguf"}, []string{"m-Q8_0.gguf"}},
		{"onnx/", []string{"onnx/a", "onnx/b/c"}, []string{"onnxa", "x/onnx/a"}},
		{"model-0000[12]-of-*.safetensors", []string{"model-00001-of-3.safetensors"}, []string{"model-00003-of-3.safetensors"}},
		{"file?.bin", []string{"file1.bin"}, []string{"file10.bin"}},
		{"a+b(c).txt", []string{"a+b(c).txt"}, []string{"aab(c).txt"}},
	}
	for _, tt := range tests {
		res, err := compileGlobs([]string{tt.pattern})
		if err != nil {
			t.Fatalf("%q: %v", tt.pattern, err)
		}
		for _, s := range tt.match {
			if !matchAny(s, res) {
				t.Errorf("%q should match %q", tt.pattern, s)
			}
		}
		for _, s := range tt.noMatch {
			if matchAny(s, res) {
				t.Errorf("%q should not match %q", tt.pattern, s)
			}
		}
	}
}
