package torrent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	infohash_v2 "github.com/anacrolix/torrent/types/infohash-v2"
	"golang.org/x/time/rate"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// ErrNotFullyKept means some files of the revision aren't in the store, so
// it can't be seeded yet.
var ErrNotFullyKept = errors.New("revision is not fully kept")

// StoreFiles lists the revision's files as torrent inputs read from the
// store. Every file must be kept.
func StoreFiles(st *store.Store, m *manifest.Manifest) ([]File, error) {
	var missing []string
	files := make([]File, 0, len(m.Files))
	for _, f := range m.Files {
		if f.SHA256 == "" || !st.Has(f.SHA256) {
			missing = append(missing, f.Path)
			continue
		}
		sum := f.SHA256
		files = append(files, File{Path: f.Path, Size: f.Size, Open: func() (io.ReadCloser, error) { return st.Open(sum) }})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %d file(s) missing, e.g. %s", ErrNotFullyKept, len(missing), missing[0])
	}
	return files, nil
}

// CachePath is where the torrent for repo@commit is kept once built.
func CachePath(root string, repo manifest.Repo, commit string) string {
	parts := append([]string{root, "torrents", repo.Type + "s"}, strings.Split(repo.ID, "/")...)
	return filepath.Join(append(parts, commit+".torrent")...)
}

// ForRevision builds the torrent for a fully kept revision, or loads it
// from the cache. Building reads every file once, so the result is cached.
// The build also re-checks every file's SHA-256 against the manifest.
func ForRevision(st *store.Store, m *manifest.Manifest, opts Options) ([]byte, error) {
	cache := CachePath(st.Root(), m.Repo, m.Commit)
	if b, err := os.ReadFile(cache); err == nil {
		return b, nil
	}
	files, err := StoreFiles(st, m)
	if err != nil {
		return nil, err
	}
	res, err := Build(m.Commit, files, opts)
	if err != nil {
		return nil, err
	}
	for _, f := range m.Files {
		if got := fmt.Sprintf("%x", res.SHA256[f.Path]); got != f.SHA256 {
			return nil, fmt.Errorf("%s: store content hashes to %s, manifest says %s; run weightkeep verify", f.Path, got, f.SHA256)
		}
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		return nil, err
	}
	tmp := cache + ".tmp"
	if err := os.WriteFile(tmp, res.MetaInfo, 0o644); err != nil {
		return nil, err
	}
	return res.MetaInfo, os.Rename(tmp, cache)
}

// ClientConfig configures the BitTorrent client.
type ClientConfig struct {
	// DataDir holds the client's own state (DHT table and so on).
	DataDir    string
	ListenPort int // 0 picks a free port
	NoDHT      bool
	// UploadRate caps upload in bytes per second; 0 means no cap.
	UploadRate int
	// NoWebSeeds disables fetching from url-list entries.
	NoWebSeeds bool
	Log        *slog.Logger
}

// Client seeds kept revisions straight from the store: torrent files map
// onto the content-addressed blobs, so nothing is copied.
type Client struct {
	cl *torrent.Client
	st *store.Store

	mu       sync.Mutex
	storages []storage.ClientImplCloser // closed with the client; anacrolix doesn't
}

func (c *Client) track(s storage.ClientImplCloser) storage.ClientImplCloser {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storages = append(c.storages, s)
	return s
}

// NewClient starts a BitTorrent client over st.
func NewClient(st *store.Store, cfg ClientConfig) (*Client, error) {
	tc := torrent.NewDefaultClientConfig()
	tc.DataDir = cfg.DataDir
	tc.Seed = true
	tc.ListenPort = cfg.ListenPort
	tc.NoDHT = cfg.NoDHT
	tc.DisableWebseeds = cfg.NoWebSeeds
	if cfg.UploadRate > 0 {
		tc.UploadRateLimiter = rate.NewLimiter(rate.Limit(cfg.UploadRate), max(cfg.UploadRate, 256<<10))
	}
	if cfg.Log != nil {
		tc.Slogger = cfg.Log
	}
	cl, err := torrent.NewClient(tc)
	if err != nil {
		return nil, fmt.Errorf("start BitTorrent client: %w", err)
	}
	return &Client{cl: cl, st: st}, nil
}

// Close stops the client and closes the per-torrent storage it opened, which
// releases file handles on blobs (Windows can't delete or move open files).
func (c *Client) Close() error {
	errs := c.cl.Close()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.storages {
		errs = append(errs, s.Close())
	}
	c.storages = nil
	return errors.Join(errs...)
}

// Addr is the address peers can reach this client on.
func (c *Client) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: c.cl.LocalPort()}
}

// Seed adds a fully kept revision for seeding with its torrent (from
// ForRevision). The torrent must be piece-aligned (Options.V1Only, ADR 0010).
func (c *Client) Seed(m *manifest.Manifest, metaInfo []byte) (*torrent.Torrent, error) {
	mi, err := metainfo.Load(strings.NewReader(string(metaInfo)))
	if err != nil {
		return nil, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, err
	}
	if info.Name != m.Commit {
		return nil, fmt.Errorf("torrent is for %s, not %s", info.Name, m.Commit)
	}
	blobPath := map[string]string{}
	for _, f := range m.Files {
		if !c.st.Has(f.SHA256) {
			return nil, fmt.Errorf("%w: %s", ErrNotFullyKept, f.Path)
		}
		blobPath[f.Path] = c.st.Path(f.SHA256)
	}
	stor, err := newBlobStorage(&info, blobPath)
	if err != nil {
		return nil, err
	}
	ih := mi.HashInfoBytes()

	opts := torrent.AddTorrentOpts{
		InfoHash:                 ih,
		Storage:                  stor,
		InfoBytes:                mi.InfoBytes,
		DisableInitialPieceCheck: true,
	}
	if info.HasV2() {
		opts.InfoHashV2 = g.Some(infohash_v2.HashBytes(mi.InfoBytes))
	}
	t, _ := c.cl.AddTorrentOpt(opts)
	if len(mi.PieceLayers) > 0 {
		if errs := t.AddPieceLayers(mi.PieceLayers); len(errs) > 0 {
			return nil, fmt.Errorf("add piece layers: %w", errors.Join(errs...))
		}
	}
	if len(mi.UrlList) > 0 {
		t.AddWebSeeds(mi.UrlList)
	}
	return t, nil
}

// Download fetches a torrent into dir (not the store): used by tests and by
// the swarm fetcher, which verifies and imports into the store afterwards.
func (c *Client) Download(ctx context.Context, metaInfo []byte, dir string, peers ...net.Addr) (*torrent.Torrent, error) {
	mi, err := metainfo.Load(strings.NewReader(string(metaInfo)))
	if err != nil {
		return nil, err
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, err
	}
	spec.Storage = c.track(storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   dir,
		TorrentDirMaker: func(base string, _ *metainfo.Info, _ metainfo.Hash) string { return base },
		PieceCompletion: storage.NewMapPieceCompletion(),
	}))
	t, _, err := c.cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, err
	}
	var pi []torrent.PeerInfo
	for _, a := range peers {
		pi = append(pi, torrent.PeerInfo{Addr: a, Trusted: true})
	}
	t.AddPeers(pi)
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	t.DownloadAll()
	select {
	case <-t.Complete().On():
		return t, nil
	case <-ctx.Done():
		return t, ctx.Err()
	}
}
