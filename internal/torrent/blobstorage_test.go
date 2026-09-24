package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// Every piece read through the seeding storage, padding included, must hash
// to the piece hash in the torrent.
func TestBlobStoragePiecesMatchTorrent(t *testing.T) {
	st, m := keptRevision(t, sampleFiles(64<<10))
	meta, err := ForRevision(st, m, Options{PieceLength: 64 << 10, V1Only: true})
	if err != nil {
		t.Fatal(err)
	}
	mi, err := metainfo.Load(bytes.NewReader(meta))
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string]string{}
	for _, f := range m.Files {
		blobs[f.Path] = st.Path(f.SHA256)
	}
	s, err := newAlignedStorage(&info, func(p string) (string, bool) { b, ok := blobs[p]; return b, ok }, false)
	if err != nil {
		t.Fatal(err)
	}
	ti, err := s.OpenTorrent(context.Background(), &info, mi.HashInfoBytes())
	if err != nil {
		t.Fatal(err)
	}
	for i := range info.NumPieces() {
		p := info.Piece(i)
		buf := make([]byte, p.V1Length())
		if _, err := ti.Piece(p).ReadAt(buf, 0); err != nil {
			t.Fatalf("piece %d: %v", i, err)
		}
		sum := sha1.Sum(buf)
		if !bytes.Equal(sum[:], p.V1Hash().Unwrap().Bytes()) {
			t.Errorf("piece %d doesn't match its hash", i)
		}
	}
	if _, err := ti.Piece(info.Piece(0)).WriteAt([]byte("x"), 0); err == nil {
		t.Error("seeding storage accepted a write")
	}
}

func TestBlobStorageRejectsBadTorrents(t *testing.T) {
	unpadded := metainfo.Info{PieceLength: 16 << 10, Pieces: make([]byte, 20), Files: []metainfo.FileInfo{
		{Length: 100, Path: []string{"a"}},
		{Length: 100, Path: []string{"b"}}, // starts mid-piece
	}}
	any := func(string) (string, bool) { return "x", true }
	if _, err := newAlignedStorage(&unpadded, any, false); err == nil {
		t.Error("accepted an unpadded torrent")
	}
	tooFewPieces := metainfo.Info{PieceLength: 16 << 10, Files: []metainfo.FileInfo{{Length: 100, Path: []string{"a"}}}}
	if _, err := newAlignedStorage(&tooFewPieces, any, false); err == nil {
		t.Error("accepted a torrent with fewer piece hashes than data")
	}
}
