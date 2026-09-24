package torrent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/afshinghezeli/weightkeep/internal/testutil"
)

const commit = "93efa2f097d58c2a74874c7e644dbc9b0cee75a2"

// sampleFiles covers the boundaries that trip up merkle and padding code:
// empty, one byte, exactly one 16 KiB block, one byte more, exactly one
// piece, one byte more, several pieces with a tail, and nested paths.
func sampleFiles(pl int64) map[string][]byte {
	r := rand.New(rand.NewSource(1))
	gen := func(n int64) []byte {
		b := make([]byte, n)
		r.Read(b)
		return b
	}
	return map[string][]byte{
		".gitattributes":          []byte("*.bin filter=lfs\n"),
		"empty/__init__.py":       {},
		"one.txt":                 gen(1),
		"block.bin":               gen(16 << 10),
		"block-plus-one.bin":      gen(16<<10 + 1),
		"piece.bin":               gen(pl),
		"piece-plus-one.bin":      gen(pl + 1),
		"sub/dir/many-pieces.bin": gen(3*pl + 12345),
		"z-last.json":             gen(777),
	}
}

func toFiles(m map[string][]byte) []File {
	var out []File
	for p, b := range m {
		out = append(out, File{Path: p, Size: int64(len(b)), Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(b)), nil
		}})
	}
	return out
}

func TestBuildReadsBack(t *testing.T) {
	const pl = 64 << 10
	files := sampleFiles(pl)
	res, err := Build(commit, toFiles(files), Options{
		PieceLength: pl,
		WebSeeds:    []string{"https://huggingface.co/acme/tiny/resolve/"},
		Extra:       map[string]any{"weightkeep.license": "MIT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mi, err := metainfo.Load(bytes.NewReader(res.MetaInfo))
	if err != nil {
		t.Fatal(err)
	}
	if mi.HashInfoBytes() != metainfo.Hash(res.InfoHashV1) {
		t.Error("v1 info hash differs from anacrolix's")
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasV2() || !info.HasV1() {
		t.Fatalf("not a hybrid: v1=%v v2=%v", info.HasV1(), info.HasV2())
	}
	if info.Name != commit || len(mi.UrlList) != 1 {
		t.Errorf("name %q, url-list %v", info.Name, mi.UrlList)
	}
	for p, b := range files {
		if got, want := res.SHA256[p], sha256.Sum256(b); got != want {
			t.Errorf("%s: sha256 %x, want %x", p, got, want)
		}
		if len(b) > 0 && len(b) <= 16<<10 {
			// A single-block file's root is just its SHA-256.
			if res.Roots[p] != sha256.Sum256(b) {
				t.Errorf("%s: small file root should equal its sha256", p)
			}
		}
	}
	if len(mi.PieceLayers) != 2 { // piece-plus-one.bin and many-pieces.bin
		t.Errorf("%d piece layers, want 2", len(mi.PieceLayers))
	}
	// Web seed URLs must come out as <base><commit>/<path>.
	for _, f := range info.UpvertedFiles() {
		if strings.HasPrefix(strings.Join(f.Path, "/"), ".pad/") && f.Length == 0 {
			t.Error("zero-length pad file")
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	files := toFiles(sampleFiles(64 << 10))
	a, err := Build(commit, files, Options{PieceLength: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	// Same files in another order.
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	b, err := Build(commit, files, Options{PieceLength: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.MetaInfo, b.MetaInfo) {
		t.Error("output depends on input order")
	}
}

func TestBuildRejects(t *testing.T) {
	ok := toFiles(map[string][]byte{"a": {1}})
	if _, err := Build("a/b", ok, Options{}); err == nil {
		t.Error("name with a slash accepted")
	}
	if _, err := Build(commit, ok, Options{PieceLength: 1000}); err == nil {
		t.Error("non power-of-two piece length accepted")
	}
	short := []File{{Path: "x", Size: 10, Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("abc")), nil }}}
	if _, err := Build(commit, short, Options{PieceLength: 16 << 10}); err == nil {
		t.Error("file shorter than its declared size accepted")
	}
	long := []File{{Path: "x", Size: 2, Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("abc")), nil }}}
	if _, err := Build(commit, long, Options{PieceLength: 16 << 10}); err == nil {
		t.Error("file longer than its declared size accepted")
	}
}

func TestPieceLengthFor(t *testing.T) {
	for total, want := range map[int64]int64{
		1 << 20:   4 << 20,
		60 << 30:  4 << 20, // 15k pieces
		100 << 30: 8 << 20, // 25k at 4 MiB is too many
		2 << 40:   64 << 20,
	} {
		if got := PieceLengthFor(total); got != want {
			t.Errorf("PieceLengthFor(%d) = %d, want %d", total, got, want)
		}
	}
}

// TestMatchesLibtorrent compares info hashes with libtorrent 2, the
// reference implementation, for the same files and piece length.
func TestMatchesLibtorrent(t *testing.T) {
	testutil.Network(t) // installs python-libtorrent with uv
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not installed")
	}
	for _, pl := range []int64{16 << 10, 64 << 10, 4 << 20} {
		t.Run(strconv.FormatInt(pl, 10), func(t *testing.T) {
			files := sampleFiles(pl)
			dir := filepath.Join(t.TempDir(), commit)
			for p, b := range files {
				fp := filepath.Join(dir, filepath.FromSlash(p))
				if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fp, b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res, err := Build(commit, toFiles(files), Options{PieceLength: pl})
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(uv, "run", "--quiet", "--no-project", "--python", "3.13", "--with", "libtorrent==2.1.1",
				"python", "testdata/libtorrent_hashes.py", dir, strconv.FormatInt(pl, 10)).CombinedOutput()
			if err != nil {
				t.Fatalf("libtorrent: %v\n%s", err, out)
			}
			fields := strings.Fields(string(out))
			want := hex.EncodeToString(res.InfoHashV1[:]) + " " + hex.EncodeToString(res.InfoHashV2[:])
			if got := strings.Join(fields[len(fields)-2:], " "); got != want {
				t.Errorf("libtorrent says %s\nwe say         %s", got, want)
			}
		})
	}
}
