package torrent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// alignedStorage is torrent storage for piece-aligned torrents (every file
// padded to a piece boundary, ADR 0010), where each piece is one file's
// bytes followed by zeros. It replaces anacrolix's file storage, which kept
// files open and had platform-specific part-file behaviour.
//
// Every read and write opens the file, does its work and closes it, so no
// handle outlives a request. Padding always reads as zeros, whatever a peer
// sent for it.
//
// Read-only mode serves store blobs for seeding: writes are refused and
// every piece reports complete, because blobs were verified when stored.
// Writable mode downloads into plain files and tracks completion itself.
type alignedStorage struct {
	pieces   []pieceLoc
	writable bool

	mu   sync.Mutex
	done map[int]bool
}

type pieceLoc struct {
	path    string // file holding this piece's data
	fileOff int64  // where the piece starts in that file
	data    int64  // bytes of file data in the piece; the rest is padding
}

var errReadOnly = errors.New("seeding storage is read-only")

// newAlignedStorage maps every piece of info onto the file it belongs to.
// pathFor gives the on-disk path for a repo path.
func newAlignedStorage(info *metainfo.Info, pathFor func(repoPath string) (string, bool), writable bool) (*alignedStorage, error) {
	pl := info.PieceLength
	s := &alignedStorage{pieces: make([]pieceLoc, info.NumPieces()), writable: writable, done: map[int]bool{}}
	assigned := make([]bool, len(s.pieces))
	for f := range info.UpvertedV1Files() {
		if f.Attr == "p" || f.Length == 0 {
			continue
		}
		if f.TorrentOffset%pl != 0 {
			return nil, fmt.Errorf("%v doesn't start on a piece boundary; only padded torrents are supported", f.Path)
		}
		repoPath := joinPath(f.BestPath())
		path, ok := pathFor(repoPath)
		if !ok {
			return nil, fmt.Errorf("no file for %s", repoPath)
		}
		first := int(f.TorrentOffset / pl)
		for i := first; int64(i-first)*pl < f.Length; i++ {
			if i >= len(s.pieces) {
				return nil, fmt.Errorf("torrent has %d piece hashes, too few for its files", len(s.pieces))
			}
			off := int64(i-first) * pl
			s.pieces[i] = pieceLoc{path: path, fileOff: off, data: min(pl, f.Length-off)}
			assigned[i] = true
		}
	}
	for i, ok := range assigned {
		if !ok {
			return nil, fmt.Errorf("piece %d belongs to no file", i)
		}
	}
	return s, nil
}

func (s *alignedStorage) OpenTorrent(context.Context, *metainfo.Info, metainfo.Hash) (storage.TorrentImpl, error) {
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl { return &alignedPiece{s: s, i: p.Index()} },
		Close: func() error { return nil },
	}, nil
}

type alignedPiece struct {
	s *alignedStorage
	i int
}

func (p *alignedPiece) loc() pieceLoc { return p.s.pieces[p.i] }

func (p *alignedPiece) ReadAt(b []byte, off int64) (int, error) {
	loc := p.loc()
	n := 0
	if off < loc.data {
		f, err := os.Open(loc.path)
		if err != nil {
			return 0, err
		}
		want := min(int64(len(b)), loc.data-off)
		n, err = f.ReadAt(b[:want], loc.fileOff+off)
		f.Close()
		if err != nil {
			return n, err
		}
	}
	clear(b[n:]) // padding
	return len(b), nil
}

func (p *alignedPiece) WriteAt(b []byte, off int64) (int, error) {
	if !p.s.writable {
		return 0, errReadOnly
	}
	loc := p.loc()
	if off < loc.data {
		if err := os.MkdirAll(filepath.Dir(loc.path), 0o755); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(loc.path, os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			return 0, err
		}
		want := min(int64(len(b)), loc.data-off)
		_, werr := f.WriteAt(b[:want], loc.fileOff+off)
		cerr := f.Close()
		if err := errors.Join(werr, cerr); err != nil {
			return 0, err
		}
	}
	return len(b), nil // bytes past the data are padding: accepted, not stored
}

func (p *alignedPiece) MarkComplete() error    { return p.set(true) }
func (p *alignedPiece) MarkNotComplete() error { return p.set(false) }

func (p *alignedPiece) set(done bool) error {
	if !p.s.writable {
		return nil
	}
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	p.s.done[p.i] = done
	return nil
}

func (p *alignedPiece) Completion() storage.Completion {
	if !p.s.writable {
		return storage.Completion{Complete: true, Ok: true}
	}
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return storage.Completion{Complete: p.s.done[p.i], Ok: true}
}

func joinPath(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "/"
		}
		out += p
	}
	return out
}
