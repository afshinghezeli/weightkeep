package manifest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/store"
)

func sample() *Manifest {
	return &Manifest{
		Version:   FormatVersion,
		Repo:      Repo{Type: "model", ID: "acme/tiny"},
		Commit:    strings.Repeat("a", 40),
		FetchedAt: time.Date(2026, 9, 24, 10, 30, 15, 123456789, time.FixedZone("CEST", 2*3600)),
		Upstream:  "https://huggingface.co",
		License:   License{IDs: []string{"apache-2.0"}},
		Files: []File{
			{Path: "model.safetensors", Size: 100, SHA256: strings.Repeat("b", 64), GitSHA1: strings.Repeat("c", 40), LFS: true},
			{Path: "config.json", Size: 10, SHA256: strings.Repeat("d", 64), GitSHA1: strings.Repeat("e", 40)},
		},
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCanonicalIsStable(t *testing.T) {
	a, err := sample().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	// Same content, files in the other order, same instant in another zone.
	m := sample()
	m.Files[0], m.Files[1] = m.Files[1], m.Files[0]
	m.FetchedAt = m.FetchedAt.UTC()
	b, err := m.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("canonical forms differ:\n%s\n%s", a, b)
	}
	want := `{"version":1,"repo":{"type":"model","id":"acme/tiny"},"commit":"` + strings.Repeat("a", 40) +
		`","fetched_at":"2026-09-24T08:30:15Z","upstream":"https://huggingface.co","license":{"ids":["apache-2.0"]},"files":[` +
		`{"path":"config.json","size":10,"sha256":"` + strings.Repeat("d", 64) + `","git_sha1":"` + strings.Repeat("e", 40) + `","lfs":false},` +
		`{"path":"model.safetensors","size":100,"sha256":"` + strings.Repeat("b", 64) + `","git_sha1":"` + strings.Repeat("c", 40) + `","lfs":true}]}` + "\n"
	if string(a) != want {
		t.Errorf("canonical form changed. If intentional, it needs a format version bump.\ngot  %s\nwant %s", a, want)
	}
}

func TestParseRoundTrip(t *testing.T) {
	data, err := sample().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Errorf("round trip changed the bytes:\n%s\n%s", data, again)
	}
	if f, ok := m.File("config.json"); !ok || f.ETag() != strings.Repeat("e", 40) {
		t.Errorf("File(config.json) = %+v, %v", f, ok)
	}
	if f, ok := m.File("model.safetensors"); !ok || f.ETag() != strings.Repeat("b", 64) {
		t.Errorf("LFS ETag should be the sha256: %+v", f)
	}
	if _, ok := m.File("nope"); ok {
		t.Error("File found a missing path")
	}
	if m.Size() != 110 {
		t.Errorf("Size = %d", m.Size())
	}
}

func TestParseRejects(t *testing.T) {
	good, _ := sample().Canonical()
	tests := map[string]string{
		"newer version":  strings.Replace(string(good), `"version":1`, `"version":2`, 1),
		"unknown field":  strings.Replace(string(good), `"upstream"`, `"surprise":1,"upstream"`, 1),
		"path traversal": strings.Replace(string(good), `"path":"config.json"`, `"path":"../../etc/passwd"`, 1),
		"bad commit":     strings.Replace(string(good), strings.Repeat("a", 40), "main", 1),
		"bad repo id":    strings.Replace(string(good), `"acme/tiny"`, `"../tiny"`, 1),
		"duplicate path": strings.Replace(string(good), `"path":"model.safetensors"`, `"path":"config.json"`, 1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Errorf("Parse accepted %s", data)
			}
		})
	}
}

func TestSaveLoadList(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	m := sample()
	if err := Save(ctx, st, m); err != nil {
		t.Fatal(err)
	}
	// Saving again replaces rather than duplicates.
	if err := Save(ctx, st, m); err != nil {
		t.Fatal(err)
	}

	got, err := Load(ctx, st, m.Repo, m.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.Files[0].Path != "config.json" {
		t.Errorf("Load = %+v", got)
	}

	list, err := List(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is in the blob store in this test, so nothing counts as kept.
	if len(list) != 1 || list[0].Files != 2 || list[0].Size != 110 || list[0].KeptFiles != 0 || list[0].Commit != m.Commit {
		t.Errorf("List = %+v", list)
	}

	refs, err := ReferencedBlobs(ctx, st)
	if err != nil || len(refs) != 2 || !refs[strings.Repeat("b", 64)] {
		t.Errorf("ReferencedBlobs = %v, %v", refs, err)
	}

	if _, err := Load(ctx, st, m.Repo, strings.Repeat("f", 40)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load of an unknown commit: %v", err)
	}
	if err := Delete(ctx, st, m.Repo, m.Commit); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, st, m.Repo, m.Commit); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load after Delete: %v", err)
	}
	if refs, _ := ReferencedBlobs(ctx, st); len(refs) != 0 {
		t.Errorf("files rows survived Delete: %v", refs)
	}
	if _, err := os.Stat(Path(st.Root(), m.Repo, m.Commit)); !os.IsNotExist(err) {
		t.Errorf("manifest file survived Delete: %v", err)
	}
}

func TestRefs(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	repo := Repo{Type: "model", ID: "acme/tiny"}
	c1, c2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	t1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	if _, _, err := ResolveRef(ctx, st, repo, "main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref: %v", err)
	}
	if err := SetRef(ctx, st, repo, "main", c1, t1); err != nil {
		t.Fatal(err)
	}
	if err := SetRef(ctx, st, repo, "main", c2, t1.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	commit, at, err := ResolveRef(ctx, st, repo, "main")
	if err != nil || commit != c2 || !at.Equal(t1.Add(time.Hour)) {
		t.Errorf("ResolveRef = %s, %v, %v", commit, at, err)
	}

	// A kept commit resolves to itself without a ref row.
	m := sample()
	if err := Save(ctx, st, m); err != nil {
		t.Fatal(err)
	}
	if commit, _, err := ResolveRef(ctx, st, m.Repo, m.Commit); err != nil || commit != m.Commit {
		t.Errorf("ResolveRef(commit) = %s, %v", commit, err)
	}
}

func TestDiff(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{Files: []File{
			{Path: "a", Size: 1, GitSHA1: "g1", SHA256: "s1"},
			{Path: "b", Size: 2, GitSHA1: "g2", SHA256: "s2", LFS: true},
		}}
	}
	if d := Diff(base(), base()); len(d) != 0 {
		t.Errorf("identical: %v", d)
	}
	noSHA := base()
	noSHA.Files[0].SHA256 = "" // not downloaded yet
	if d := Diff(base(), noSHA); len(d) != 0 {
		t.Errorf("missing sha256 on one side: %v", d)
	}
	for name, change := range map[string]func(m *Manifest){
		"size":    func(m *Manifest) { m.Files[1].Size = 3 },
		"git id":  func(m *Manifest) { m.Files[0].GitSHA1 = "gx" },
		"sha256":  func(m *Manifest) { m.Files[1].SHA256 = "sx" },
		"missing": func(m *Manifest) { m.Files = m.Files[:1] },
		"extra":   func(m *Manifest) { m.Files = append(m.Files, File{Path: "c"}) },
	} {
		got := base()
		change(got)
		if d := Diff(base(), got); len(d) != 1 {
			t.Errorf("%s: %v", name, d)
		}
	}
}
