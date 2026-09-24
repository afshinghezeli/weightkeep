// Package torrent builds and serves the torrents weightkeep shares. This
// file is the builder: hybrid BitTorrent v1+v2 torrents (BEP 3, BEP 52, with
// BEP 47 padding) for one repo revision, named after the commit, with the
// Hub's resolve URL as a BEP 19 web seed. See ADR 0005.
//
// anacrolix/torrent can read v2 torrents but only builds v1, hence this.
// The output is checked byte for byte against libtorrent in the tests.
package torrent

import (
	"crypto/sha1" //nolint:gosec // G505: BitTorrent v1 piece hashes are SHA-1 by specification
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
)

// File is one file of the revision, in repo path form ("a/b.bin").
type File struct {
	Path string
	Size int64
	Open func() (io.ReadCloser, error)
}

// Options for Build.
type Options struct {
	// PieceLength must be a power of two, at least 16 KiB. Zero picks one
	// from the total size (see PieceLengthFor).
	PieceLength int64
	// WebSeeds become the url-list (BEP 19). For a Hub web seed this is
	// "https://huggingface.co/<repo>/resolve/", so each file's URL is
	// .../resolve/<commit>/<path>.
	WebSeeds []string
	// Extra top-level metainfo keys, e.g. "weightkeep.license".
	Extra map[string]any
	// Comment and CreatedBy go into the metainfo as usual.
	Comment   string
	CreatedBy string
	// V1Only leaves out the v2 file tree and piece layers. The v1 part is
	// the same: sorted files, each padded to a piece boundary, as libtorrent
	// makes with v1_only|canonical_files. See ADR 0010 for why this is the
	// default for seeding for now.
	V1Only bool
}

// Result is a built torrent.
type Result struct {
	MetaInfo   []byte // the .torrent file
	InfoHashV1 [20]byte
	InfoHashV2 [32]byte
	// Roots is each file's BEP 52 "pieces root" (absent for empty files).
	Roots map[string][32]byte
	// SHA256 is each file's plain SHA-256, computed in the same pass.
	SHA256      map[string][32]byte
	PieceLength int64
}

// Magnet returns a magnet link, with the v2 hash too for hybrid torrents.
func (r *Result) Magnet(name string) string {
	if r.InfoHashV2 == ([32]byte{}) {
		return fmt.Sprintf("magnet:?xt=urn:btih:%x&dn=%s", r.InfoHashV1, name)
	}
	return fmt.Sprintf("magnet:?xt=urn:btih:%x&xt=urn:btmh:1220%x&dn=%s", r.InfoHashV1, r.InfoHashV2, name)
}

// PieceLengthFor picks a piece length for a revision of total bytes: at
// least 4 MiB (few resolver requests per file on Hub web seeds, small piece
// layers), growing to keep the count around 20k pieces, at most 64 MiB.
func PieceLengthFor(total int64) int64 {
	l := int64(4 << 20)
	for l < 64<<20 && total/l > 20_000 {
		l *= 2
	}
	return l
}

// Build creates a hybrid torrent named name (the commit id) from files.
// Each file is read once.
func Build(name string, files []File, opts Options) (*Result, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, fmt.Errorf("torrent name %q must be a single path segment", name)
	}
	if len(files) == 0 {
		return nil, errors.New("no files")
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	pl := opts.PieceLength
	if pl == 0 {
		pl = PieceLengthFor(total)
	}
	if pl < merkle.BlockSize || pl&(pl-1) != 0 {
		return nil, fmt.Errorf("piece length %d must be a power of two of at least %d", pl, merkle.BlockSize)
	}

	// v2 orders files by path components in bencode (byte) order; the v1
	// file list must follow the same order for the hybrid to be valid.
	files = append([]File(nil), files...)
	sort.Slice(files, func(i, j int) bool { return pathLess(files[i].Path, files[j].Path) })
	for i := 1; i < len(files); i++ {
		if files[i].Path == files[i-1].Path {
			return nil, fmt.Errorf("duplicate path %q", files[i].Path)
		}
	}

	res := &Result{Roots: map[string][32]byte{}, SHA256: map[string][32]byte{}, PieceLength: pl}
	v1 := &v1Pieces{length: pl}
	var v1Files []map[string]any
	fileTree := map[string]any{}
	pieceLayers := map[string]string{}

	for _, f := range files {
		root, layer, sum, err := hashFile(f, pl, v1)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		res.SHA256[f.Path] = sum
		parts := strings.Split(f.Path, "/")

		leaf := map[string]any{"length": f.Size}
		if f.Size > 0 {
			leaf["pieces root"] = string(root[:])
			res.Roots[f.Path] = root
			if f.Size > pl {
				pieceLayers[string(root[:])] = layer
			}
		}
		insert(fileTree, parts, map[string]any{"": leaf})
		v1Files = append(v1Files, map[string]any{"length": f.Size, "path": parts})

		// Pad so every file ends on a piece boundary (BEP 47), the last one
		// included: libtorrent 2 does this for hybrid torrents, and the v1
		// info hash has to match what it produces for the same files.
		if rem := f.Size % pl; rem != 0 {
			pad := pl - rem
			v1Files = append(v1Files, map[string]any{"attr": "p", "length": pad, "path": []string{".pad", strconv.FormatInt(pad, 10)}})
			v1.writeZeros(pad)
		}
	}
	v1.finish()

	info := map[string]any{
		"files":        v1Files,
		"name":         name,
		"piece length": pl,
		"pieces":       string(v1.pieces),
	}
	if !opts.V1Only {
		info["file tree"] = fileTree
		info["meta version"] = 2
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, err
	}
	res.InfoHashV1 = sha1.Sum(infoBytes) //nolint:gosec // G401: v1 infohash is SHA-1
	if !opts.V1Only {
		res.InfoHashV2 = sha256.Sum256(infoBytes)
	}

	mi := map[string]any{"info": bencode.Bytes(infoBytes)}
	if len(pieceLayers) > 0 && !opts.V1Only {
		mi["piece layers"] = pieceLayers
	}
	if len(opts.WebSeeds) > 0 {
		mi["url-list"] = opts.WebSeeds
	}
	if opts.Comment != "" {
		mi["comment"] = opts.Comment
	}
	if opts.CreatedBy != "" {
		mi["created by"] = opts.CreatedBy
	}
	for k, v := range opts.Extra {
		mi[k] = v
	}
	if res.MetaInfo, err = bencode.Marshal(mi); err != nil {
		return nil, err
	}
	return res, nil
}

// hashFile reads f once and returns its v2 pieces root, its piece layer
// (concatenated piece hashes, for files longer than a piece), and its plain
// SHA-256, while feeding the v1 piece hasher.
func hashFile(f File, pl int64, v1 *v1Pieces) (root [32]byte, layer string, sum [32]byte, err error) {
	if f.Size == 0 {
		return root, "", sha256.Sum256(nil), nil
	}
	rc, err := f.Open()
	if err != nil {
		return root, "", sum, err
	}
	defer rc.Close()

	whole := sha256.New()
	var pieceHashes [][32]byte
	buf := make([]byte, 1<<20)
	var read int64
	for read < f.Size {
		// One piece at a time: its v2 hash is the merkle root of its blocks.
		ph := merkle.NewHash()
		want := min(pl, f.Size-read)
		n, err := io.CopyBuffer(io.MultiWriter(ph, whole, v1), io.LimitReader(rc, want), buf)
		if err != nil {
			return root, "", sum, err
		}
		if n != want {
			return root, "", sum, fmt.Errorf("file is shorter than its size %d", f.Size)
		}
		read += n
		var h [32]byte
		if f.Size > pl {
			// Tail pieces are padded with zero leaves to the full piece.
			copy(h[:], ph.SumMinLength(nil, int(pl)))
		} else {
			copy(h[:], ph.Sum(nil))
		}
		pieceHashes = append(pieceHashes, h)
	}
	if extra, _ := rc.Read(buf[:1]); extra > 0 {
		return root, "", sum, fmt.Errorf("file is longer than its size %d", f.Size)
	}
	copy(sum[:], whole.Sum(nil))

	if f.Size <= pl {
		// A single piece's merkle root is the file's root.
		return pieceHashes[0], "", sum, nil
	}
	root = merkle.RootWithPadHash(pieceHashes, padHash(pl))
	var b strings.Builder
	for _, h := range pieceHashes {
		b.Write(h[:])
	}
	return root, b.String(), sum, nil
}

// padHash is the root of a piece-sized subtree of zero leaves, used to pad
// the piece layer up to a power of two.
func padHash(pl int64) [32]byte {
	var h [32]byte // zero leaf
	for n := pl / merkle.BlockSize; n > 1; n /= 2 {
		h = sha256.Sum256(append(h[:], h[:]...))
	}
	return h
}

// v1Pieces hashes the concatenation of all files (and pad files) into
// SHA-1 pieces.
type v1Pieces struct {
	length int64
	cur    []byte
	pieces []byte
}

func (v *v1Pieces) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		take := min(int(v.length)-len(v.cur), len(p))
		v.cur = append(v.cur, p[:take]...)
		p = p[take:]
		if int64(len(v.cur)) == v.length {
			v.flush()
		}
	}
	return n, nil
}

func (v *v1Pieces) writeZeros(n int64) {
	zeros := make([]byte, min(n, 1<<20))
	for n > 0 {
		k := min(n, int64(len(zeros)))
		_, _ = v.Write(zeros[:k])
		n -= k
	}
}

func (v *v1Pieces) flush() {
	s := sha1.Sum(v.cur) //nolint:gosec // G401: v1 piece hash
	v.pieces = append(v.pieces, s[:]...)
	v.cur = v.cur[:0]
}

func (v *v1Pieces) finish() {
	if len(v.cur) > 0 {
		v.flush()
	}
}

func insert(tree map[string]any, parts []string, leaf map[string]any) {
	for _, p := range parts[:len(parts)-1] {
		next, ok := tree[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			tree[p] = next
		}
		tree = next
	}
	tree[parts[len(parts)-1]] = leaf
}

// pathLess orders paths component by component, as bencode dictionaries
// order the v2 file tree.
func pathLess(a, b string) bool {
	pa, pb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}
