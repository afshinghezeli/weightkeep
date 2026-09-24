package keep

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/torrent"
)

// Node A keeps and seeds a revision; node B, whose Hub is unreachable, pulls
// it from A with nothing but a magnet link and A's address.
func TestPullFromAnotherNodeWhileHubIsDown(t *testing.T) {
	ctx := context.Background()
	a, _ := newKeeper(t)
	res, err := a.Pull(ctx, PullRequest{Repo: tinyRep})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := torrent.ForRevision(a.Store, res.Manifest, torrent.Options{PieceLength: 16 << 10, V1Only: true})
	if err != nil {
		t.Fatal(err)
	}
	seeder, err := torrent.NewClient(a.Store, torrent.ClientConfig{DataDir: t.TempDir(), NoDHT: true, NoWebSeeds: true})
	if err != nil {
		t.Fatal(err)
	}
	defer seeder.Close()
	if _, err := seeder.Seed(res.Manifest, meta); err != nil {
		t.Fatal(err)
	}
	mi, _ := metainfo.Load(bytes.NewReader(meta))
	magnet, err := mi.MagnetV2()
	if err != nil {
		t.Fatal(err)
	}

	// Node B: its upstream refuses everything.
	bStore, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bStore.Close()
	deadHub, _ := hub.New("http://127.0.0.1:1", hub.Options{})
	b := &Keeper{Store: bStore, Hub: deadHub, Fetcher: &fetch.Fetcher{Store: bStore, Hub: deadHub, Retries: 1, Backoff: time.Millisecond}}
	if _, err := b.Pull(ctx, PullRequest{Repo: tinyRep}); err == nil || !hub.Retryable(err) {
		t.Fatalf("the Hub should be unreachable for node B: %v", err)
	}
	leecher, err := torrent.NewClient(bStore, torrent.ClientConfig{DataDir: t.TempDir(), NoDHT: true, NoWebSeeds: true})
	if err != nil {
		t.Fatal(err)
	}
	defer leecher.Close()

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	want := manifest.Repo{Type: "model", ID: tinyRep.ID}
	got, err := b.PullTorrent(cctx, TorrentPull{Client: leecher, Source: torrent.Source{Magnet: magnet.String()}, Peers: []net.Addr{seeder.Addr()}, Want: &want})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has two byte-identical files; the second is present once
	// the first is stored.
	if n := len(got.Downloaded) + len(got.Present); n != len(res.Manifest.Files) {
		t.Errorf("downloaded %d + present %d of %d files", len(got.Downloaded), len(got.Present), len(res.Manifest.Files))
	}
	for _, f := range res.Manifest.Files {
		if !bStore.Has(f.SHA256) {
			t.Errorf("%s missing on node B", f.Path)
		}
	}
	if _, err := manifest.Load(ctx, bStore, want, res.Manifest.Commit); err != nil {
		t.Errorf("node B has no manifest: %v", err)
	}

	// Asking for a different repo than the torrent carries is refused.
	other := manifest.Repo{Type: "model", ID: "acme/other"}
	_, err = b.PullTorrent(cctx, TorrentPull{Client: leecher, Source: torrent.Source{MetaInfo: meta}, Peers: []net.Addr{seeder.Addr()}, Want: &other})
	if err == nil {
		t.Error("pulled a torrent for the wrong repo")
	}
}
