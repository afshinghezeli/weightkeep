package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

type denyRepo string

func (d denyRepo) Denied(m *manifest.Manifest) (string, bool) {
	return "author opt-out", m.Repo.ID == string(d)
}

func TestDenylistedRevisionsStayOnThisMachine(t *testing.T) {
	h := newHarness(t, Options{})
	h.k.Deny = denyRepo("acme/tiny")
	srv := New(h.k, Options{})

	get := func(path, remote string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	// This machine's own clients still get it.
	if w := get("/acme/tiny/resolve/main/config.json", "127.0.0.1:50000"); w.Code != http.StatusOK {
		t.Fatalf("loopback: %d %s", w.Code, w.Body)
	}
	if w := get("/api/models/acme/tiny", "[::1]:50000"); w.Code != http.StatusOK {
		t.Fatalf("loopback v6: %d %s", w.Code, w.Body)
	}
	// Another machine doesn't, by name or by content digest.
	for _, path := range []string{
		"/acme/tiny/resolve/main/config.json",
		"/api/models/acme/tiny/tree/main",
		"/v2/acme/tiny/blobs/sha256:" + cfgFile.SHA256(),
		"/blobs/sha256/" + cfgFile.SHA256(),
	} {
		w := get(path, "192.0.2.7:40000")
		if w.Code != http.StatusUnavailableForLegalReasons || w.Header().Get("X-Error-Code") != "Denylisted" {
			t.Errorf("%s from another machine: %d %q", path, w.Code, w.Header().Get("X-Error-Code"))
		}
	}
	// Other repos are unaffected.
	h.k.Deny = denyRepo("acme/other")
	if w := get("/acme/tiny/resolve/main/config.json", "192.0.2.7:40000"); w.Code != http.StatusOK {
		t.Errorf("not denylisted: %d %s", w.Code, w.Body)
	}
}
