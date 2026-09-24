// Command webseed-spike checks the web seed design of ADR 0005 against the
// real Hub: a torrent whose name is a commit id and whose url-list is the
// repo's /resolve/ base, downloaded by anacrolix/torrent from web seeds only
// (no peers, no DHT, no trackers), throttled so the transfer outlasts the
// Hub's signed CDN URLs (about an hour).
//
// It is a one-off experiment kept for the record, not part of weightkeep.
//
//	weightkeep export HuggingFaceTB/SmolLM2-135M --to /tmp/smol
//	go run ./tools/webseed-spike -src /tmp/smol -repo HuggingFaceTB/SmolLM2-135M \
//	    -commit 93efa2f097d58c2a74874c7e644dbc9b0cee75a2 -rate 60000
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

type loggingTransport struct {
	base    http.RoundTripper
	limiter *rate.Limiter
	start   time.Time

	mu     sync.Mutex
	counts map[string]int
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "weightkeep-webseed-spike")
	resp, err := t.base.RoundTrip(req)
	status := "error"
	if err == nil {
		status = resp.Status
		resp.Body = &slowBody{ReadCloser: resp.Body, l: t.limiter}
	}
	t.mu.Lock()
	key := req.URL.Host + " " + status
	t.counts[key]++
	n := t.counts[key]
	t.mu.Unlock()
	if n <= 3 || n%50 == 0 {
		//nolint:gosec // G706: logs URLs this tool built itself
		log.Printf("+%s %s %s%s range=%q -> %s (#%d)", time.Since(t.start).Round(time.Second), req.Method,
			req.URL.Host, trim(req.URL.Path), req.Header.Get("Range"), status, n)
	}
	return resp, err
}

func trim(p string) string {
	if len(p) > 60 {
		return p[:60] + "…"
	}
	return p
}

type slowBody struct {
	io.ReadCloser
	l *rate.Limiter
}

func (b *slowBody) Read(p []byte) (int, error) {
	if len(p) > 16<<10 {
		p = p[:16<<10]
	}
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_ = b.l.WaitN(context.Background(), n)
	}
	return n, err
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	src := flag.String("src", "", "directory with the revision's files (from weightkeep export --to)")
	repo := flag.String("repo", "", "org/name")
	commit := flag.String("commit", "", "40-hex commit")
	bps := flag.Int("rate", 60_000, "download bytes per second")
	flag.Parse()

	info := metainfo.Info{PieceLength: 4 << 20}
	if err := info.BuildFromFilePath(*src); err != nil {
		return err
	}
	info.Name = *commit
	infoBytes, err := bencodeInfo(info)
	if err != nil {
		return err
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, UrlList: []string{"https://huggingface.co/" + *repo + "/resolve/"}}
	log.Printf("torrent %s: %d files, %d bytes, %d pieces, infohash %s", info.Name, len(info.Files), info.TotalLength(),
		info.NumPieces(), mi.HashInfoBytes().HexString())

	dir, _ := os.MkdirTemp("", "webseed-spike-")
	defer os.RemoveAll(dir)
	lt := &loggingTransport{base: http.DefaultTransport, limiter: rate.NewLimiter(rate.Limit(*bps), 64<<10),
		start: time.Now(), counts: map[string]int{}}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.NoDHT, cfg.DisableTrackers, cfg.NoUpload, cfg.Seed = true, true, true, false
	cfg.ListenPort = 0
	cfg.WebTransport = lt
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return err
	}
	defer cl.Close()
	t, err := cl.AddTorrent(&mi)
	if err != nil {
		return err
	}
	<-t.GotInfo()
	t.DownloadAll()
	total := t.Length()
	for t.BytesCompleted() < total {
		time.Sleep(time.Minute)
		log.Printf("+%s %d/%d bytes (%.1f%%)", time.Since(lt.start).Round(time.Second), t.BytesCompleted(), total,
			100*float64(t.BytesCompleted())/float64(total))
	}
	log.Printf("done in %s; request counts by host and status:", time.Since(lt.start).Round(time.Second))
	for k, v := range lt.counts {
		fmt.Printf("  %-60s %d\n", k, v)
	}
	// Every piece was checked against the torrent's SHA-1 piece hashes, and
	// the files match the Hub's since the torrent was built from verified blobs.
	return nil
}

func bencodeInfo(info metainfo.Info) ([]byte, error) {
	return bencode.Marshal(info)
}
