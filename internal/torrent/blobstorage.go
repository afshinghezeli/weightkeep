package torrent

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// blobStorage is read-only torrent storage over store blobs, for seeding.
//
// Seeded torrents are v1 with every file padded to a piece boundary (ADR
// 0010), so each piece is one file's bytes followed by zeros. A read opens
// the blob, reads and closes it: no handle outlives a request, no memory
// mapping, nothing to clean up, and writes are refused so blobs stay intact.
type blobStorage struct {
	pieces []pieceLoc
}

type pieceLoc struct {
	path    string // blob holding this piece's data
	fileOff int64  // where the piece starts in that blob
	data    int64  // bytes of file data in the piece; the rest is padding
}

var errReadOnly = errors.New("seeding storage is read-only")

// newBlobStorage maps every piece of info onto the blob of the file it
// belongs to. blobPath gives the blob for a repo path.
func newBlobStorage(info *metainfo.Info, blobPath map[string]string) (*blobStorage, error) {
	pl := info.PieceLength
	s := &blobStorage{pieces: make([]pieceLoc, info.NumPieces())}
	assigned := make([]bool, len(s.pieces))
	for f := range info.UpvertedV1Files() {
		if f.Attr == "p" || f.Length == 0 {
			continue
		}
		if f.TorrentOffset%pl != 0 {
			return nil, fmt.Errorf("%v doesn't start on a piece boundary; seeding needs padded torrents", f.Path)
		}
		path := joinPath(f.BestPath())
		blob, ok := blobPath[path]
		if !ok {
			return nil, fmt.Errorf("no blob for %s", path)
		}
		first := int(f.TorrentOffset / pl)
		for i := first; int64(i-first)*pl < f.Length; i++ {
			if i >= len(s.pieces) {
				return nil, fmt.Errorf("torrent has %d piece hashes, too few for its files", len(s.pieces))
			}
			off := int64(i-first) * pl
			s.pieces[i] = pieceLoc{path: blob, fileOff: off, data: min(pl, f.Length-off)}
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

func (s *blobStorage) OpenTorrent(context.Context, *metainfo.Info, metainfo.Hash) (storage.TorrentImpl, error) {
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl { return blobPiece(s.pieces[p.Index()]) },
		Close: func() error { return nil },
	}, nil
}

type blobPiece pieceLoc

func (p blobPiece) ReadAt(b []byte, off int64) (int, error) {
	n := 0
	if off < p.data {
		f, err := os.Open(p.path)
		if err != nil {
			return 0, err
		}
		want := min(int64(len(b)), p.data-off)
		n, err = f.ReadAt(b[:want], p.fileOff+off)
		f.Close()
		if err != nil {
			return n, err
		}
	}
	// Past the file's end, the piece is BEP 47 padding: zeros.
	clear(b[n:])
	return len(b), nil
}

func (blobPiece) WriteAt([]byte, int64) (int, error) { return 0, errReadOnly }
func (blobPiece) MarkComplete() error                { return nil }
func (blobPiece) MarkNotComplete() error             { return nil }

// Completion reports every piece complete: blobs were verified when they
// were stored, and `weightkeep verify` re-checks them.
func (blobPiece) Completion() storage.Completion {
	return storage.Completion{Complete: true, Ok: true}
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
