package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

var gguf = fakehub.File{Path: "model-Q4_K_M.gguf", Content: bytes.Repeat([]byte("GGUF"), 50_000), LFS: true}

// pullLikeOllama walks the protocol the way ollama's server/images.go and
// download.go do: manifest, HEAD each layer, GET following same-host
// redirects only, require a 307 or a 200 with Location, then ranged GETs to
// that direct URL without credentials.
func pullLikeOllama(t *testing.T, base, name, tag string) map[string][]byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/v2/"+name+"/manifests/"+tag, nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Set("User-Agent", "ollama/0.12.0 (arm64 darwin) Go/go1.24")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Config struct{ Digest string }
		Layers []struct {
			Digest string
			Size   int64
		}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("manifest: %d %s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	sameHost := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Hostname() != via[0].URL.Hostname() {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	blobs := map[string][]byte{}
	digests := []string{m.Config.Digest}
	for _, l := range m.Layers {
		digests = append(digests, l.Digest)
	}
	for _, d := range digests {
		head, err := http.Head(base + "/v2/" + name + "/blobs/" + d)
		if err != nil {
			t.Fatal(err)
		}
		head.Body.Close()
		if head.StatusCode != http.StatusOK || head.ContentLength <= 0 {
			t.Fatalf("HEAD %s: %d, length %d", d, head.StatusCode, head.ContentLength)
		}
		get, err := sameHost.Get(base + "/v2/" + name + "/blobs/" + d)
		if err != nil {
			t.Fatal(err)
		}
		get.Body.Close()
		loc, err := get.Location() // "http: no Location header in response" is what ollama reports
		if (get.StatusCode != http.StatusTemporaryRedirect && get.StatusCode != http.StatusOK) || err != nil {
			t.Fatalf("GET %s: %d, Location: %v", d, get.StatusCode, err)
		}
		// Two ranged parts, as ollama splits big blobs.
		half := head.ContentLength / 2
		var data []byte
		ranges := []string{
			fmt.Sprintf("bytes=0-%d", half-1),
			fmt.Sprintf("bytes=%d-%d", half, head.ContentLength-1),
		}
		for _, rng := range ranges {
			req, _ := http.NewRequest(http.MethodGet, loc.String(), nil)
			req.Header.Set("Range", rng)
			part, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(part.Body)
			part.Body.Close()
			data = append(data, b...)
		}
		blobs[d] = data
	}
	return blobs
}

func TestOllamaPull(t *testing.T) {
	h := newHarness(t, Options{})
	h.up.Add(&fakehub.Repo{ID: "acme/gguf", License: "apache-2.0", Files: []fakehub.File{cfgFile, gguf}})

	blobs := pullLikeOllama(t, h.proxy.URL, "acme/gguf", "Q4_K_M")
	if got := blobs["sha256:"+gguf.SHA256()]; !bytes.Equal(got, gguf.Content) {
		t.Fatalf("model layer: %d bytes, want %d", len(got), len(gguf.Content))
	}
	if len(blobs) != 2 {
		t.Errorf("got %d blobs, want config + model", len(blobs))
	}
	// The model layer is the same blob huggingface_hub would use.
	if !h.st.Has(gguf.SHA256()) {
		t.Error("GGUF not kept in the store")
	}

	// Offline, the same pull works from what was kept.
	off := httptest.NewServer(New(h.k, Options{Offline: true}))
	defer off.Close()
	h.up.ResetRequests()
	again := pullLikeOllama(t, off.URL, "acme/gguf", "Q4_K_M")
	if !bytes.Equal(again["sha256:"+gguf.SHA256()], gguf.Content) {
		t.Error("offline Ollama pull returned different bytes")
	}
	if n := len(h.up.Requests()); n != 0 {
		t.Errorf("offline pull made %d upstream requests", n)
	}
}

func TestOllamaUnknownTagRelaysError(t *testing.T) {
	h := newHarness(t, Options{})
	h.up.Add(&fakehub.Repo{ID: "acme/gguf", Files: []fakehub.File{gguf}})
	resp, body := h.do(t, http.MethodGet, "/v2/acme/gguf/manifests/Q9_Z", nil)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "quantization") {
		t.Errorf("unknown tag: %d %s", resp.StatusCode, body)
	}
	resp, _ = h.do(t, http.MethodGet, "/blobs/sha256/"+strings.Repeat("0", 64), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown direct blob: %d", resp.StatusCode)
	}
}
