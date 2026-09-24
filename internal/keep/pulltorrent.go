package keep

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/torrent"
)

// TorrentPull describes a pull from other weightkeep nodes over BitTorrent.
type TorrentPull struct {
	Client *torrent.Client
	Source torrent.Source
	Peers  []net.Addr
	// Want, if set, must match the revision the torrent carries.
	Want *manifest.Repo
}

// PullTorrent keeps a revision fetched from the swarm instead of the Hub.
// The torrent's embedded manifest, authenticated by its info hash, gives
// every file's SHA-256; each file is checked against it as it's stored.
func (k *Keeper) PullTorrent(ctx context.Context, req TorrentPull) (*PullResult, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	dir := filepath.Join(k.Store.Root(), "tmp", "torrent-"+hex.EncodeToString(rnd[:]))
	defer os.RemoveAll(dir)

	infoBytes, err := req.Client.Fetch(ctx, req.Source, dir, req.Peers, k.Store.Has)
	if err != nil {
		return nil, err
	}
	m, err := torrent.EmbeddedManifest(infoBytes, k.now(), "bittorrent")
	if err != nil {
		return nil, err
	}
	if req.Want != nil && (m.Repo.ID != req.Want.ID || m.Repo.Type != req.Want.Type) {
		return nil, fmt.Errorf("the torrent is for %s, not %s", m.Repo, *req.Want)
	}

	res := &PullResult{Manifest: m}
	for _, f := range m.Files {
		if k.Store.Has(f.SHA256) {
			res.Present = append(res.Present, f.Path)
			continue
		}
		exp := store.Expect{SHA256: f.SHA256, Size: f.Size, HasSize: true}
		if !f.LFS {
			exp.GitSHA1 = f.GitSHA1
		}
		if err := k.importFile(ctx, filepath.Join(dir, m.Commit, filepath.FromSlash(f.Path)), exp); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		res.Downloaded = append(res.Downloaded, f.Path)
	}
	if err := manifest.Save(ctx, k.Store, m); err != nil {
		return nil, err
	}
	return res, nil
}

func (k *Keeper) importFile(ctx context.Context, path string, exp store.Expect) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = k.Store.Put(ctx, f, exp)
	return err
}
