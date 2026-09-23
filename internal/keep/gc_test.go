package keep

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

func TestGC(t *testing.T) {
	k, _ := newKeeper(t)
	ctx := context.Background()
	res, err := k.Pull(ctx, PullRequest{Repo: tinyRep})
	if err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	k.Now = func() time.Time { return later }

	// An orphan blob (from a deleted revision) and a fresh orphan (a pull in
	// progress that hasn't saved its manifest yet).
	old, err := k.Store.Put(ctx, bytes.NewReader([]byte("orphan")), store.Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(k.Store.Path(old.SHA256), later.Add(-48*time.Hour), later.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh, err := k.Store.Put(ctx, bytes.NewReader([]byte("in flight")), store.Expect{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(k.Store.Path(fresh.SHA256), later.Add(-time.Minute), later.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Tidy the referenced blobs' times so only the orphans are candidates.
	for _, f := range res.Manifest.Files {
		if err := os.Chtimes(k.Store.Path(f.SHA256), later.Add(-72*time.Hour), later.Add(-72*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// An abandoned partial from last month and an old lock file.
	tmp := filepath.Join(k.Store.Root(), "tmp")
	stale := filepath.Join(tmp, old.SHA256[:40]+".part")
	if err := os.WriteFile(stale, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, later.Add(-30*24*time.Hour), later.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	dry, err := k.GC(ctx, GCOptions{Grace: DefaultGrace, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Blobs) != 1 || !k.Store.Has(old.SHA256) {
		t.Fatalf("dry run: %+v, and it must not delete", dry)
	}

	got, err := k.GC(ctx, GCOptions{Grace: DefaultGrace})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blobs) != 1 || got.Blobs[0].SHA256 != old.SHA256 || got.Protected != 1 {
		t.Errorf("gc = %+v", got)
	}
	if k.Store.Has(old.SHA256) || !k.Store.Has(fresh.SHA256) {
		t.Error("gc removed the wrong blob")
	}
	for _, f := range res.Manifest.Files {
		if !k.Store.Has(f.SHA256) {
			t.Errorf("gc removed referenced %s", f.Path)
		}
	}
	if got.Tmp.Partials != 1 || got.Tmp.Locks == 0 {
		t.Errorf("tmp cleanup = %+v", got.Tmp)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale partial survived")
	}

	// After forgetting the revision, its blobs go too (except shared ones).
	if err := manifest.Delete(ctx, k.Store, manifest.Repo{Type: "model", ID: tinyRep.ID}, res.Manifest.Commit); err != nil {
		t.Fatal(err)
	}
	got, err = k.GC(ctx, GCOptions{Grace: DefaultGrace})
	if err != nil {
		t.Fatal(err)
	}
	// 6 files, but Q4_K_M and Q4_K_L share one blob.
	if len(got.Blobs) != 5 {
		t.Errorf("removed %d blobs after deleting the revision, want 5", len(got.Blobs))
	}
}

func TestGCLeavesHeldPartials(t *testing.T) {
	k, _ := newKeeper(t)
	ctx := context.Background()
	p, err := k.Store.Partial(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", store.Expect{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	k.Now = func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }
	got, err := k.GC(ctx, GCOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tmp.Partials != 0 {
		t.Error("gc removed a partial another writer holds")
	}
}
