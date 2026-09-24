package torrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// cacheFormat changes whenever what goes into a seeded torrent changes, so
// torrents cached by an older weightkeep are rebuilt instead of reused.
// 2: manifest embedded in the info dict.
const cacheFormat = 2

// CachePath is where the torrent for repo@commit is kept once built. The
// name records what the info hash depends on besides the files.
func CachePath(root string, repo manifest.Repo, commit string, opts Options) string {
	parts := append([]string{root, "torrents", repo.Type + "s"}, strings.Split(repo.ID, "/")...)
	kind := "hybrid"
	if opts.V1Only {
		kind = "v1"
	}
	name := fmt.Sprintf("%s.f%d.%s.%d.torrent", commit, cacheFormat, kind, opts.PieceLength)
	return filepath.Join(append(parts, name)...)
}

// ForRevision builds the torrent for a fully kept revision, or loads it
// from the cache. Building reads every file once, so the result is cached.
// The build also re-checks every file's SHA-256 against the manifest, and
// the manifest is embedded in the info dict.
func ForRevision(st *store.Store, m *manifest.Manifest, opts Options) ([]byte, error) {
	files, err := StoreFiles(st, m)
	if err != nil {
		return nil, err
	}
	if opts.PieceLength == 0 {
		var total int64
		for _, f := range files {
			total += f.Size
		}
		opts.PieceLength = PieceLengthFor(total)
	}
	cache := CachePath(st.Root(), m.Repo, m.Commit, opts)
	if b, err := os.ReadFile(cache); err == nil {
		return b, nil
	}
	// Seeded torrents always carry the manifest (see embed.go).
	extra := ManifestInfoExtra(m)
	for k, v := range opts.InfoExtra {
		extra[k] = v
	}
	opts.InfoExtra = extra
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

// Close stops the client. The storages it uses hold no open files.
func (c *Client) Close() error {
	return errors.Join(c.cl.Close()...)
}

// Addr is the address peers can reach this client on.
func (c *Client) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: c.cl.LocalPort()}
}

// Seed adds a fully kept revision for seeding with its torrent (from
// ForRevision). The torrent must be piece-aligned (Options.V1Only, ADR 0010).
func (c *Client) Seed(m *manifest.Manifest, metaInfo []byte) (*torrent.Torrent, error) {
	mi, err := metainfo.Load(bytes.NewReader(metaInfo))
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
	stor, err := newAlignedStorage(&info, func(p string) (string, bool) { b, ok := blobPath[p]; return b, ok }, false)
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

// Source is where a torrent comes from: a .torrent file's bytes or a
// magnet link.
type Source struct {
	MetaInfo []byte
	Magnet   string
}

// Fetch downloads a revision's torrent into dir and returns its info bytes.
// Files whose SHA-256 (from the embedded manifest) satisfies have are not
// downloaded: torrents are piece-aligned, so a file can be skipped whole.
// The torrent's web seeds are used when the client allows them.
func (c *Client) Fetch(ctx context.Context, src Source, dir string, peers []net.Addr, have func(sha256 string) bool) ([]byte, error) {
	var spec *torrent.TorrentSpec
	switch {
	case src.MetaInfo != nil:
		mi, err := metainfo.Load(bytes.NewReader(src.MetaInfo))
		if err != nil {
			return nil, err
		}
		if spec, err = torrent.TorrentSpecFromMetaInfoErr(mi); err != nil {
			return nil, err
		}
	case src.Magnet != "":
		var err error
		if spec, err = torrent.TorrentSpecFromMagnetUri(src.Magnet); err != nil {
			return nil, fmt.Errorf("magnet link: %w", err)
		}
	default:
		return nil, errors.New("no torrent or magnet link")
	}
	spec.Storage = &lazyAligned{dir: dir}
	t, _, err := c.cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, err
	}
	defer t.Drop()
	var pi []torrent.PeerInfo
	for _, a := range peers {
		pi = append(pi, torrent.PeerInfo{Addr: a, Trusted: true})
	}
	t.AddPeers(pi)
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for torrent metadata from peers: %w", ctx.Err())
	}
	infoBytes := t.Metainfo().InfoBytes
	m, err := EmbeddedManifest(infoBytes, time.Time{}, "")
	if err != nil {
		return nil, err
	}
	shaOf := map[string]string{}
	for _, f := range m.Files {
		shaOf[f.Path] = f.SHA256
	}
	var need []*torrent.File
	for _, f := range t.Files() {
		p := strings.TrimPrefix(f.Path(), t.Name()+"/")
		sum, ok := shaOf[p]
		if !ok || f.Length() == 0 || have(sum) {
			f.SetPriority(torrent.PiecePriorityNone)
			continue
		}
		f.Download()
		need = append(need, f)
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		done := true
		for _, f := range need {
			if f.BytesCompleted() < f.Length() {
				done = false
				break
			}
		}
		if done {
			return infoBytes, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// lazyAligned builds a writable alignedStorage when anacrolix opens the
// torrent, which for magnet links is only once the info has arrived.
type lazyAligned struct{ dir string }

func (l *lazyAligned) OpenTorrent(ctx context.Context, info *metainfo.Info, ih metainfo.Hash) (storage.TorrentImpl, error) {
	root := filepath.Join(l.dir, info.BestName())
	pathFor := func(p string) (string, bool) { return filepath.Join(root, filepath.FromSlash(p)), true }
	s, err := newAlignedStorage(info, pathFor, true)
	if err != nil {
		return storage.TorrentImpl{}, err
	}
	// Empty files have no pieces, so nothing would ever write them.
	for f := range info.UpvertedV1Files() {
		if f.Length == 0 && f.Attr != "p" {
			p, _ := pathFor(joinPath(f.BestPath()))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return storage.TorrentImpl{}, err
			}
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				return storage.TorrentImpl{}, err
			}
		}
	}
	return s.OpenTorrent(ctx, info, ih)
}

// Download fetches a whole torrent into dir. Used by tests.
func (c *Client) Download(ctx context.Context, metaInfo []byte, dir string, peers ...net.Addr) (*torrent.Torrent, error) {
	_, err := c.Fetch(ctx, Source{MetaInfo: metaInfo}, dir, peers, func(string) bool { return false })
	return nil, err
}
