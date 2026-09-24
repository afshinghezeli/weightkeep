package keep

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

// withMirror returns a keeper whose primary Hub is primary and whose only
// mirror serves the usual fixture repo.
func withMirror(t *testing.T, primary *fakehub.Hub) (*Keeper, *fakehub.Hub) {
	t.Helper()
	mirror := fakehub.New(&fakehub.Repo{ID: tinyRep.ID, License: "apache-2.0", Files: []fakehub.File{cfg, lic, q4, q8}})
	t.Cleanup(mirror.Close)
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	pc, _ := hub.New(primary.URL, hub.Options{})
	mc, _ := hub.New(mirror.URL, hub.Options{})
	return &Keeper{
		Store: st, Hub: pc, Mirrors: []*hub.Client{mc},
		Fetcher: &fetch.Fetcher{Store: st, Hub: pc, Retries: 1, Backoff: time.Millisecond},
	}, mirror
}

func TestMirrorWhenTheRepoIsGone(t *testing.T) {
	primary := fakehub.New() // the repo was deleted
	defer primary.Close()
	k, mirror := withMirror(t, primary)
	res, err := k.Pull(context.Background(), PullRequest{Repo: tinyRep})
	if err != nil {
		t.Fatal(err)
	}
	if res.Manifest.Upstream != mirror.URL {
		t.Errorf("manifest upstream = %s, want the mirror %s", res.Manifest.Upstream, mirror.URL)
	}
	var cdn int
	for _, r := range mirror.Requests() {
		if r.Host == "cdn" {
			cdn++
		}
	}
	if cdn == 0 {
		t.Error("large files weren't fetched from the mirror")
	}
}

func TestMirrorWhenTheHubIsDown(t *testing.T) {
	primary := fakehub.New()
	primary.Close() // nothing listens any more
	k, _ := withMirror(t, primary)
	if _, err := k.Pull(context.Background(), PullRequest{Repo: tinyRep}); err != nil {
		t.Fatalf("with the Hub down and a mirror configured: %v", err)
	}
}

func TestNoMirrorForGatedRepos(t *testing.T) {
	primary := fakehub.New(&fakehub.Repo{ID: tinyRep.ID, Gated: "manual", Files: []fakehub.File{cfg}})
	defer primary.Close()
	k, mirror := withMirror(t, primary)
	_, err := k.Pull(context.Background(), PullRequest{Repo: tinyRep})
	if !errors.Is(err, hub.ErrGated) {
		t.Fatalf("err = %v, want the Hub's gated answer", err)
	}
	if n := len(mirror.Requests()); n != 0 {
		t.Errorf("asked the mirror %d times for a gated repo", n)
	}
}
