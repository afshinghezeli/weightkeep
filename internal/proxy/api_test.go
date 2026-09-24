package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/hub"
)

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

func TestRepoInfo(t *testing.T) {
	h := newHarness(t, Options{})
	for _, path := range []string{"/api/models/acme/tiny", "/api/models/acme/tiny/revision/main"} {
		resp, body := h.do(t, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
		}
		info := decode[map[string]any](t, body)
		if info["id"] != "acme/tiny" || !hub.IsCommit(info["sha"].(string)) {
			t.Errorf("%s: %v", path, info)
		}
		if sibs := info["siblings"].([]any); len(sibs) != 3 {
			t.Errorf("siblings = %v", sibs)
		}
		if strings.Contains(string(body), "xetHash") {
			t.Error("xetHash leaked")
		}
	}

	_, body := h.do(t, http.MethodGet, "/api/models/acme/tiny/revision/main?blobs=true", nil)
	info := decode[struct {
		Siblings []struct {
			RFilename string `json:"rfilename"`
			BlobID    string `json:"blobId"`
			Size      int64  `json:"size"`
			LFS       *struct {
				SHA256      string `json:"sha256"`
				PointerSize int    `json:"pointerSize"`
			} `json:"lfs"`
		} `json:"siblings"`
	}](t, body)
	for _, s := range info.Siblings {
		if s.RFilename == "model.safetensors" && (s.LFS == nil || s.LFS.SHA256 != weights.SHA256() || s.LFS.PointerSize == 0) {
			t.Errorf("blobs=true LFS sibling = %+v", s)
		}
		if s.RFilename == "config.json" && s.BlobID != cfgFile.GitSHA1() {
			t.Errorf("blobs=true regular sibling = %+v", s)
		}
	}
}

func TestTreeMatchesUpstream(t *testing.T) {
	h := newHarness(t, Options{})
	resp, body := h.do(t, http.MethodGet, "/api/models/acme/tiny/tree/main?recursive=true", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode, string(body))
	}
	ours := decode[[]map[string]any](t, body)

	// Same listing straight from the fake Hub, minus xetHash.
	up, err := http.Get(h.up.URL + "/api/models/acme/tiny/tree/main?recursive=true")
	if err != nil {
		t.Fatal(err)
	}
	var theirs []map[string]any
	if err := json.NewDecoder(up.Body).Decode(&theirs); err != nil {
		t.Fatal(err)
	}
	up.Body.Close()
	files := func(list []map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, e := range list {
			if e["type"] == "file" {
				delete(e, "xetHash")
				out[e["path"].(string)] = e
			}
		}
		return out
	}
	a, b := files(ours), files(theirs)
	if len(a) != len(b) {
		t.Fatalf("%d files vs upstream %d", len(a), len(b))
	}
	for p, e := range b {
		got, _ := json.Marshal(a[p])
		want, _ := json.Marshal(e)
		if string(got) != string(want) {
			t.Errorf("%s:\n ours     %s\n upstream %s", p, got, want)
		}
	}
}

func TestTreeNonRecursiveAndSubpath(t *testing.T) {
	h := newHarness(t, Options{})
	_, body := h.do(t, http.MethodGet, "/api/models/acme/tiny/tree/main", nil)
	top := decode[[]treeEntry](t, body)
	var names []string
	for _, e := range top {
		names = append(names, e.Type+":"+e.Path)
	}
	if strings.Join(names, ",") != "file:config.json,file:model.safetensors,directory:pkg" {
		t.Errorf("top level = %v", names)
	}
	_, body = h.do(t, http.MethodGet, "/api/models/acme/tiny/tree/main/pkg", nil)
	sub := decode[[]treeEntry](t, body)
	if len(sub) != 1 || sub[0].Path != "pkg/__init__.py" {
		t.Errorf("pkg/ = %+v", sub)
	}
	resp, _ := h.do(t, http.MethodGet, "/api/models/acme/tiny/tree/main/config.json", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tree of a file: %d, want 404", resp.StatusCode)
	}
}

func TestTreePagination(t *testing.T) {
	h := newHarness(t, Options{})
	// Follow Link headers, as huggingface_hub does.
	var got []string
	next := "/api/models/acme/tiny/tree/main?recursive=true&limit=2"
	for i := 0; next != "" && i < 10; i++ {
		resp, body := h.do(t, http.MethodGet, next, nil)
		for _, e := range decode[[]treeEntry](t, body) {
			got = append(got, e.Path)
		}
		next = ""
		if link := resp.Header.Get("Link"); link != "" {
			u := strings.Trim(strings.SplitN(link, ";", 2)[0], "<>")
			if !strings.HasPrefix(u, h.proxy.URL) {
				t.Fatalf("Link points elsewhere: %s", u)
			}
			next = strings.TrimPrefix(u, h.proxy.URL)
		}
	}
	if strings.Join(got, ",") != "config.json,model.safetensors,pkg,pkg/__init__.py" {
		t.Errorf("paged listing = %v", got)
	}

	// text-generation-webui builds its own cursor and stops on an empty page.
	cursor := encodeCursor("pkg/__init__.py", 50)
	_, body := h.do(t, http.MethodGet, "/api/models/acme/tiny/tree/main?recursive=true&cursor="+url.QueryEscape(cursor), nil)
	if string(body) != "[]" {
		t.Errorf("past the end = %s, want []", body)
	}
}

func TestPathsInfo(t *testing.T) {
	h := newHarness(t, Options{})
	resp, err := http.PostForm(h.proxy.URL+"/api/models/acme/tiny/paths-info/main",
		url.Values{"paths": {"config.json", "pkg", "nope"}, "expand": {"false"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []treeEntry
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "config.json" || got[1].Type != "directory" {
		t.Errorf("paths-info = %+v", got)
	}
}

func TestRefs(t *testing.T) {
	h := newHarness(t, Options{})
	_, body := h.do(t, http.MethodGet, "/api/models/acme/tiny/refs", nil)
	refs := decode[struct {
		Branches []struct {
			Name, TargetCommit string
		}
	}](t, body)
	if len(refs.Branches) != 1 || refs.Branches[0].Name != "main" || !hub.IsCommit(refs.Branches[0].TargetCommit) {
		t.Fatalf("online refs = %s", body)
	}

	// Offline: answered from refs weightkeep has seen.
	h.do(t, http.MethodHead, "/acme/tiny/resolve/main/config.json", nil)
	off := httptest.NewServer(New(h.k, Options{Offline: true}))
	defer off.Close()
	resp, err := http.Get(off.URL + "/api/models/acme/tiny/refs")
	if err != nil {
		t.Fatal(err)
	}
	var offline struct {
		Branches []struct{ Name, TargetCommit string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&offline); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(offline.Branches) != 1 || offline.Branches[0].TargetCommit != refs.Branches[0].TargetCommit {
		t.Errorf("offline refs = %+v", offline)
	}
}

func TestOfflineInfoAndTreeFromManifest(t *testing.T) {
	h := newHarness(t, Options{})
	h.do(t, http.MethodHead, "/acme/tiny/resolve/main/config.json", nil)
	off := httptest.NewServer(New(h.k, Options{Offline: true}))
	defer off.Close()
	for _, p := range []string{"/api/models/acme/tiny/revision/main", "/api/models/acme/tiny/tree/main?recursive=true"} {
		resp, err := http.Get(off.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("offline %s: %d", p, resp.StatusCode)
		}
	}
	resp, _ := http.Get(off.URL + "/api/whoami-v2")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("offline whoami: %d", resp.StatusCode)
	}
}

func TestStripXet(t *testing.T) {
	in := `[{"path":"a","xetHash":"77","lfs":{"oid":"x","xetHash":"88"}},{"n":12345678901234567890}]`
	out := string(stripXetJSON([]byte(in)))
	if strings.Contains(out, "xetHash") || !strings.Contains(out, "12345678901234567890") {
		t.Errorf("stripXetJSON = %s", out)
	}
	link := `<https://huggingface.co/api/models/x/xet-read-token/abc>; rel="xet-auth", <https://huggingface.co/api/models/x/tree/main?cursor=1>; rel="next"`
	if got := stripXetLinks(link); strings.Contains(got, "xet") || !strings.Contains(got, `rel="next"`) {
		t.Errorf("stripXetLinks = %s", got)
	}
}
