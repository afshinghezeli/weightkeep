package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/store"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

var (
	cfgFile   = fakehub.File{Path: "config.json", Content: []byte(`{"hidden_size": 8}`)}
	weights   = fakehub.File{Path: "model.safetensors", Content: bytes.Repeat([]byte("0123456789abcdef"), 40_000), LFS: true}
	emptyFile = fakehub.File{Path: "pkg/__init__.py", Content: []byte{}}
)

type harness struct {
	up    *fakehub.Hub
	proxy *httptest.Server
	st    *store.Store
	k     *keep.Keeper
}

func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	up := fakehub.New(
		&fakehub.Repo{ID: "acme/tiny", License: "apache-2.0", Files: []fakehub.File{cfgFile, weights, emptyFile}},
		// Distinct content: identical bytes kept from another repo would be
		// served from the store without asking the Hub (dedup), which is
		// fine but not what this fixture is for.
		&fakehub.Repo{ID: "acme/gated", Gated: "manual", Files: []fakehub.File{{Path: "config.json", Content: []byte(`{"gated": true}`)}}},
	)
	t.Cleanup(up.Close)
	client, err := hub.New(up.URL, hub.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	k := &keep.Keeper{Store: st, Hub: client, Fetcher: &fetch.Fetcher{Store: st, Hub: client, Backoff: time.Millisecond}}
	srv := httptest.NewServer(New(k, opts))
	t.Cleanup(srv.Close)
	return &harness{up: up, proxy: srv, st: st, k: k}
}

func (h *harness) do(t *testing.T, method, path string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, h.proxy.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func (h *harness) cdnRequests() int {
	n := 0
	for _, r := range h.up.Requests() {
		if r.Host == "cdn" {
			n++
		}
	}
	return n
}

func noXet(t *testing.T, resp *http.Response) {
	t.Helper()
	for _, k := range []string{"X-Xet-Hash", "X-Xet-Refresh-Route", "Link"} {
		if v := resp.Header.Get(k); v != "" {
			t.Errorf("%s header leaked: %q", k, v)
		}
	}
}

func TestHeadAnswersFromManifest(t *testing.T) {
	h := newHarness(t, Options{})
	resp, _ := h.do(t, http.MethodHead, "/acme/tiny/resolve/main/model.safetensors", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if c := resp.Header.Get("X-Repo-Commit"); !hub.IsCommit(c) {
		t.Errorf("X-Repo-Commit = %q", c)
	}
	want := `"` + weights.SHA256() + `"`
	if resp.Header.Get("ETag") != want || resp.Header.Get("X-Linked-Etag") != want {
		t.Errorf("etags = %q / %q, want %s", resp.Header.Get("ETag"), resp.Header.Get("X-Linked-Etag"), want)
	}
	if resp.ContentLength != int64(len(weights.Content)) {
		t.Errorf("Content-Length = %d", resp.ContentLength)
	}
	noXet(t, resp)
	if n := h.cdnRequests(); n != 0 {
		t.Errorf("HEAD downloaded the file (%d CDN requests)", n)
	}

	resp, _ = h.do(t, http.MethodHead, "/acme/tiny/resolve/main/config.json", nil)
	if resp.Header.Get("ETag") != `"`+cfgFile.GitSHA1()+`"` {
		t.Errorf("regular file ETag = %q, want git sha1", resp.Header.Get("ETag"))
	}
}

func TestGetStreamsThenServesFromStore(t *testing.T) {
	h := newHarness(t, Options{})
	resp, body := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/model.safetensors", nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, weights.Content) {
		t.Fatalf("status %d, %d bytes", resp.StatusCode, len(body))
	}
	if !h.st.Has(weights.SHA256()) {
		t.Fatal("blob not kept after streaming")
	}
	h.up.ResetRequests()
	resp, body = h.do(t, http.MethodGet, "/acme/tiny/resolve/main/model.safetensors", map[string]string{"Range": "bytes=100-199"})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, weights.Content[100:200]) {
		t.Errorf("range from store: status %d body %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Range") != "bytes 100-199/640000" {
		t.Errorf("Content-Range = %q", resp.Header.Get("Content-Range"))
	}
	if n := len(h.up.Requests()); n != 0 {
		t.Errorf("served from store but made %d upstream requests", n)
	}
}

func TestRangeWhileStreaming(t *testing.T) {
	h := newHarness(t, Options{})
	resp, body := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/model.safetensors", map[string]string{"Range": "bytes=600000-"})
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, weights.Content[600000:]) {
		t.Fatalf("status %d, %d bytes", resp.StatusCode, len(body))
	}
	resp, _ = h.do(t, http.MethodGet, "/acme/tiny/resolve/main/config.json", map[string]string{"Range": "bytes=999999-"})
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("unsatisfiable range: %d", resp.StatusCode)
	}
}

func TestConcurrentGetsShareOneDownload(t *testing.T) {
	h := newHarness(t, Options{})
	// Learn the revision first so only the blob fetch is being counted.
	h.do(t, http.MethodHead, "/acme/tiny/resolve/main/model.safetensors", nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, body := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/model.safetensors", nil)
			if resp.StatusCode != http.StatusOK || !bytes.Equal(body, weights.Content) {
				t.Errorf("status %d, %d bytes", resp.StatusCode, len(body))
			}
		}()
	}
	wg.Wait()
	if n := h.cdnRequests(); n != 1 {
		t.Errorf("%d upstream downloads for 8 concurrent clients, want 1", n)
	}
}

func TestTamperedUpstreamNeverDeliversACompleteFile(t *testing.T) {
	h := newHarness(t, Options{})
	h.do(t, http.MethodHead, "/acme/tiny/resolve/main/config.json", nil) // learn the manifest
	bad := weights
	bad.Content = bytes.Repeat([]byte("X"), len(weights.Content))
	h.up.Add(&fakehub.Repo{ID: "acme/tiny", Files: []fakehub.File{cfgFile, bad, emptyFile}})

	resp, err := http.Get(h.proxy.URL + "/acme/tiny/resolve/main/model.safetensors")
	if err != nil {
		return // refused outright: fine
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr == nil && len(body) == len(weights.Content) {
		t.Fatalf("client received a complete %d-byte file that fails verification", len(body))
	}
	if h.st.Has(bad.SHA256()) || h.st.Has(weights.SHA256()) {
		t.Error("bad content was stored")
	}
}

func TestErrors(t *testing.T) {
	h := newHarness(t, Options{})
	tests := []struct {
		path, code string
		status     int
		commit     bool
	}{
		{"/acme/tiny/resolve/main/nope.bin", "EntryNotFound", 404, true},
		{"/acme/tiny/resolve/no-such-branch/config.json", "RevisionNotFound", 404, false},
		{"/acme/missing/resolve/main/config.json", "RepoNotFound", 404, false},
		{"/acme/gated/resolve/main/config.json", "GatedRepo", 401, false},
	}
	for _, tt := range tests {
		resp, _ := h.do(t, http.MethodGet, tt.path, nil)
		if resp.StatusCode != tt.status || resp.Header.Get("X-Error-Code") != tt.code {
			t.Errorf("%s: %d %q, want %d %q", tt.path, resp.StatusCode, resp.Header.Get("X-Error-Code"), tt.status, tt.code)
		}
		if got := hub.IsCommit(resp.Header.Get("X-Repo-Commit")); got != tt.commit {
			t.Errorf("%s: X-Repo-Commit present = %v, want %v", tt.path, got, tt.commit)
		}
	}
	resp, _ := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/..%2F..%2Fetc%2Fpasswd", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("path traversal: %d", resp.StatusCode)
	}
}

func TestEmptyFile(t *testing.T) {
	h := newHarness(t, Options{})
	for _, rng := range []string{"", "bytes=0-"} {
		resp, body := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/pkg/__init__.py", map[string]string{"Range": rng})
		if resp.StatusCode != http.StatusOK || len(body) != 0 || resp.Header.Get("Content-Range") != "" {
			t.Errorf("empty file with Range %q: %d, %d bytes, Content-Range %q", rng, resp.StatusCode, len(body), resp.Header.Get("Content-Range"))
		}
		if resp.Header.Get("X-Repo-Commit") == "" {
			t.Error("empty file response without X-Repo-Commit")
		}
	}
}

func TestUpstreamDownServesKeptRevision(t *testing.T) {
	h := newHarness(t, Options{RefTTL: time.Nanosecond})
	if resp, _ := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/model.safetensors", nil); resp.StatusCode != http.StatusOK {
		t.Fatal("warm-up failed")
	}
	h.up.Fail("/", http.StatusServiceUnavailable, 1000)
	h.k.Fetcher.Retries = 1

	resp, body := h.do(t, http.MethodGet, "/acme/tiny/resolve/main/model.safetensors", nil)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, weights.Content) {
		t.Fatalf("with the Hub down: status %d", resp.StatusCode)
	}
	if !hub.IsCommit(resp.Header.Get("X-Repo-Commit")) {
		t.Error("offline answer without X-Repo-Commit")
	}
	resp, _ = h.do(t, http.MethodGet, "/acme/other/resolve/main/config.json", nil)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Errorf("unknown repo with the Hub down: %d, want 504 (never 401/404)", resp.StatusCode)
	}
}

func TestOfflineNeverCallsUpstream(t *testing.T) {
	h := newHarness(t, Options{})
	h.do(t, http.MethodGet, "/acme/tiny/resolve/main/config.json", nil)

	off := httptest.NewServer(New(h.k, Options{Offline: true}))
	defer off.Close()
	h.up.ResetRequests()
	resp, err := http.Get(off.URL + "/acme/tiny/resolve/main/config.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("offline kept file: %d", resp.StatusCode)
	}
	resp, _ = http.Get(off.URL + "/acme/tiny/resolve/main/model.safetensors")
	resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout || !strings.Contains(resp.Header.Get("X-Error-Message"), "offline") {
		t.Errorf("offline, not kept: %d %q", resp.StatusCode, resp.Header.Get("X-Error-Message"))
	}
	if n := len(h.up.Requests()); n != 0 {
		t.Errorf("offline server made %d upstream requests", n)
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		h          string
		start, end int64
		status     int
		ok         bool
	}{
		{"", 0, 99, http.StatusOK, true},
		{"bytes=0-", 0, 99, http.StatusPartialContent, true},
		{"bytes=10-19", 10, 19, http.StatusPartialContent, true},
		{"bytes=90-500", 90, 99, http.StatusPartialContent, true},
		{"bytes=-10", 90, 99, http.StatusPartialContent, true},
		{"bytes=100-", 0, 0, 0, false},
		{"bytes=20-10", 0, 0, 0, false},
		{"bytes=0-1,5-6", 0, 99, http.StatusOK, true},
	}
	for _, tt := range tests {
		s, e, st, ok := parseRange(tt.h, 100)
		if ok != tt.ok || (ok && (s != tt.start || e != tt.end || st != tt.status)) {
			t.Errorf("parseRange(%q) = %d-%d %d %v", tt.h, s, e, st, ok)
		}
	}
}
