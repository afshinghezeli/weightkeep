package registry

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/policy"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

func record(id, commit string) *Record {
	return &Record{
		Version: FormatVersion,
		Manifest: &manifest.Manifest{
			Version: manifest.FormatVersion, Repo: manifest.Repo{Type: "model", ID: id}, Commit: commit,
			FetchedAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), Upstream: "https://huggingface.co",
			License: manifest.License{IDs: []string{"mit"}},
			Files:   []manifest.File{{Path: "config.json", Size: 2, SHA256: strings.Repeat("c", 64), GitSHA1: strings.Repeat("d", 40)}},
		},
		Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40),
		Added:  time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
	}
}

type fixture struct {
	out  string
	keys Keys
	srv  *httptest.Server
	root []byte
}

func newRegistry(t *testing.T, now time.Time, records []*Record, deny *Denylist) *fixture {
	t.Helper()
	f := &fixture{out: t.TempDir()}
	var err error
	if f.keys, err = GenerateKeys(1); err != nil {
		t.Fatal(err)
	}
	if err := Init(f.out, f.keys, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := Build(BuildInput{Out: f.out, Records: records, Denylist: deny, Keys: f.keys, Now: now}); err != nil {
		t.Fatal(err)
	}
	if f.root, err = os.ReadFile(filepath.Join(f.out, "metadata", "1.root.json")); err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewServer(http.FileServer(http.Dir(f.out)))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) client(t *testing.T) *Client {
	t.Helper()
	c, err := Open(f.srv.URL, f.root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var (
	c1 = strings.Repeat("1", 40)
	c2 = strings.Repeat("2", 40)
)

func TestSyncAndLookup(t *testing.T) {
	deny := &Denylist{Version: FormatVersion, Entries: []DenyEntry{
		{Repo: "acme/removed", Reason: "DMCA notice 2026-01", Added: time.Now()},
		{SHA256: strings.Repeat("e", 64), Reason: "author opt-out", Added: time.Now()},
	}}
	f := newRegistry(t, time.Now(), []*Record{record("acme/tiny", c1), record("acme/tiny", c2), record("other/model", c1)}, deny)
	c := f.client(t)
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	r, err := c.Record(manifest.Repo{Type: "model", ID: "acme/tiny"}, c2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest.Commit != c2 || !strings.HasPrefix(r.Magnet, "magnet:") {
		t.Errorf("record = %+v", r)
	}
	commits, err := c.Commits(manifest.Repo{Type: "model", ID: "acme/tiny"})
	if err != nil || len(commits) != 2 {
		t.Errorf("Commits = %v, %v", commits, err)
	}
	if n, _ := c.Size(); n != 3 {
		t.Errorf("Size = %d", n)
	}
	if _, err := c.Record(manifest.Repo{Type: "model", ID: "acme/tiny"}, strings.Repeat("9", 40)); err == nil {
		t.Error("found a record that isn't listed")
	}

	d, err := c.Denylist()
	if err != nil {
		t.Fatal(err)
	}
	removed := record("acme/removed", c1).Manifest
	if e, ok := d.Match(removed); !ok || e.Reason != "DMCA notice 2026-01" {
		t.Errorf("repo entry not matched: %+v %v", e, ok)
	}
	shared := record("acme/unrelated", c1).Manifest
	shared.Files[0].SHA256 = strings.Repeat("e", 64)
	if _, ok := d.Match(shared); !ok {
		t.Error("content entry not matched")
	}
	if _, ok := d.Match(record("acme/tiny", c1).Manifest); ok {
		t.Error("clean revision matched the denylist")
	}

	// Offline: a fresh client over the same cache answers without the server.
	f.srv.Close()
	offline, _ := Open(f.srv.URL, f.root, c.dir)
	if _, err := offline.Record(manifest.Repo{Type: "model", ID: "acme/tiny"}, c1); err != nil {
		t.Errorf("offline lookup after sync: %v", err)
	}
}

func TestTamperedRecordIsRejected(t *testing.T) {
	f := newRegistry(t, time.Now(), []*Record{record("acme/tiny", c1)}, nil)
	p := filepath.Join(f.out, "targets", filepath.FromSlash(RecordTarget(manifest.Repo{Type: "model", ID: "acme/tiny"}, c1)))
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), strings.Repeat("c", 64), strings.Repeat("f", 64), 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sync downloads every record and checks it against the signed targets
	// metadata, so the tampered one stops the sync.
	err := f.client(t).Sync()
	if err == nil || !strings.Contains(err.Error(), "hash verification failed") {
		t.Fatalf("tampered record: %v", err)
	}
}

func TestRollbackIsRejected(t *testing.T) {
	f := newRegistry(t, time.Now(), []*Record{record("acme/tiny", c1)}, nil)
	old := t.TempDir()
	if err := exec.Command("cp", "-R", f.out+"/.", old).Run(); err != nil {
		t.Skip("cp not available")
	}
	// Publish a newer registry (say, one that adds a denylist entry).
	deny := &Denylist{Version: FormatVersion, Entries: []DenyEntry{{Repo: "acme/tiny", Reason: "takedown", Added: time.Now()}}}
	if err := Build(BuildInput{Out: f.out, Records: []*Record{record("acme/tiny", c1)}, Denylist: deny, Keys: f.keys, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	c := f.client(t)
	if err := c.Sync(); err != nil {
		t.Fatal(err)
	}
	// A mirror now serves the older registry, without that entry.
	f.srv.Config.Handler = http.FileServer(http.Dir(old))
	c.up = nil
	if err := c.Sync(); err == nil {
		t.Fatal("accepted an older registry after seeing a newer one")
	}
}

func TestExpiredRegistryIsRejected(t *testing.T) {
	f := newRegistry(t, time.Now().Add(-8*24*time.Hour), []*Record{record("acme/tiny", c1)}, nil)
	if err := f.client(t).Sync(); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("stale registry: %v", err)
	}
	// Re-signing the timestamp (what CI does daily) fixes it.
	if err := Timestamp(f.out, f.keys, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.client(t).Sync(); err != nil {
		t.Fatalf("after re-signing the timestamp: %v", err)
	}
}

func TestSomeoneElsesRegistryIsRejected(t *testing.T) {
	ours := newRegistry(t, time.Now(), nil, nil)
	theirs := newRegistry(t, time.Now(), []*Record{record("acme/evil", c1)}, nil)
	c, _ := Open(theirs.srv.URL, ours.root, t.TempDir())
	if err := c.Sync(); err == nil {
		t.Fatal("trusted a registry signed with other keys")
	}
}

func TestDenylistValidation(t *testing.T) {
	for _, d := range []Denylist{
		{Version: FormatVersion, Entries: []DenyEntry{{Repo: "a/b"}}},
		{Version: FormatVersion, Entries: []DenyEntry{{Reason: "x"}}},
		{Version: FormatVersion, Entries: []DenyEntry{{Commit: c1, Reason: "x"}}},
		{Version: FormatVersion, Entries: []DenyEntry{{SHA256: "zz", Reason: "x"}}},
		{Version: 9},
	} {
		if err := d.Validate(); err == nil {
			t.Errorf("accepted %+v", d)
		}
	}
}

// manifestFor builds the record a node that pulled r would submit.
func manifestFor(h *fakehub.Hub, r *fakehub.Repo) *Record {
	m := &manifest.Manifest{
		Version: manifest.FormatVersion, Repo: manifest.Repo{Type: "model", ID: r.ID}, Commit: r.Commit,
		FetchedAt: time.Now().UTC(), Upstream: h.URL, License: manifest.License{IDs: []string{r.License}},
	}
	for _, f := range r.Files {
		mf := manifest.File{Path: f.Path, Size: int64(len(f.Content)), SHA256: f.SHA256(), GitSHA1: f.GitSHA1(), LFS: f.LFS}
		if f.LFS {
			mf.GitSHA1 = fakehub.File{Content: []byte(fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", f.SHA256(), len(f.Content)))}.GitSHA1()
		}
		m.Files = append(m.Files, mf)
	}
	return &Record{Version: FormatVersion, Manifest: m, Added: time.Now().UTC()}
}

func TestCheck(t *testing.T) {
	mit, _ := policy.Text("MIT")
	good := &fakehub.Repo{ID: "acme/good", License: "mit", Files: []fakehub.File{
		{Path: "LICENSE", Content: []byte(mit)},
		{Path: "config.json", Content: []byte(`{"a":1}`)},
		{Path: "model.safetensors", Content: bytes.Repeat([]byte{7}, 4096), LFS: true},
	}}
	gated := &fakehub.Repo{ID: "acme/gated", License: "mit", Gated: "manual", Files: good.Files}
	nolicence := &fakehub.Repo{ID: "acme/unlicensed", Files: good.Files[1:]}
	h := fakehub.New(good, gated, nolicence)
	t.Cleanup(h.Close)
	c, err := hub.New(h.URL, hub.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if d, err := Check(ctx, c, manifestFor(h, good), nil); err != nil || d.Tier != policy.A {
		t.Fatalf("good record: tier %v, %v", d.Tier, err)
	}

	bad := func(name string, r *Record, deny *Denylist, want string) {
		t.Helper()
		_, err := Check(ctx, c, r, deny)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error mentioning %q", name, err, want)
		}
	}
	r := manifestFor(h, good)
	r.Manifest.Files[2].SHA256 = strings.Repeat("0", 64)
	bad("wrong LFS hash", r, nil, "model.safetensors: sha256")
	r = manifestFor(h, good)
	r.Manifest.Files[1].SHA256 = strings.Repeat("0", 64)
	bad("wrong small-file hash", r, nil, "config.json: sha256")
	r = manifestFor(h, good)
	r.Manifest.Files = r.Manifest.Files[:2]
	bad("missing file", r, nil, "on the Hub but not in the record")
	r = manifestFor(h, good)
	r.Manifest.Files = append(r.Manifest.Files, manifest.File{Path: "extra.bin", Size: 1, SHA256: strings.Repeat("1", 64), GitSHA1: strings.Repeat("2", 40)})
	bad("extra file", r, nil, "in the record but not on the Hub")
	r = manifestFor(h, good)
	r.Manifest.Commit = strings.Repeat("9", 40)
	bad("unknown commit", r, nil, "")
	bad("gated", manifestFor(h, gated), nil, "tier C")
	bad("no licence", manifestFor(h, nolicence), nil, "tier C")
	bad("denylisted", manifestFor(h, good), &Denylist{Version: FormatVersion, Entries: []DenyEntry{{Repo: "acme/good", Reason: "opt-out"}}}, "denylist")
}
