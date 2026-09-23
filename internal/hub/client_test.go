package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/config"
	"github.com/afshinghezeli/weightkeep/internal/testutil"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

var (
	configJSON = fakehub.File{Path: "config.json", Content: []byte(`{"hidden_size": 128}` + "\n")}
	weights    = fakehub.File{Path: "model.safetensors", Content: []byte(strings.Repeat("w", 100_000)), LFS: true}
	nested     = fakehub.File{Path: "onnx/model.onnx", Content: []byte("onnx bytes"), LFS: true}
)

func newTestHub(t *testing.T) *fakehub.Hub {
	t.Helper()
	h := fakehub.New(
		&fakehub.Repo{ID: "acme/tiny", License: "apache-2.0", Files: []fakehub.File{configJSON, weights, nested}},
		&fakehub.Repo{ID: "acme/gated", Gated: "manual", Files: []fakehub.File{configJSON}, Tokens: []string{"hf_allowed"}},
		&fakehub.Repo{ID: "acme/gone", Disabled: true},
	)
	t.Cleanup(h.Close)
	return h
}

func newClient(t *testing.T, base, token string) *Client {
	t.Helper()
	c, err := New(base, Options{Token: config.NewToken(token), UserAgent: "weightkeep/test"})
	if err != nil {
		t.Fatal(err)
	}
	c.backoff = time.Millisecond
	return c
}

func TestRepoInfo(t *testing.T) {
	h := newTestHub(t)
	c := newClient(t, h.URL, "")
	info, err := c.RepoInfo(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !IsCommit(info.SHA) {
		t.Errorf("SHA = %q", info.SHA)
	}
	if len(info.Siblings) != 3 {
		t.Errorf("%d siblings, want 3", len(info.Siblings))
	}
	if info.Gated.IsGated() {
		t.Error("ungated repo reported as gated")
	}
	if got := []string(info.CardData.License); len(got) != 1 || got[0] != "apache-2.0" {
		t.Errorf("license = %v", got)
	}
	if len(info.Raw) == 0 {
		t.Error("Raw is empty")
	}
}

func TestRepoInfoLegacyIDRedirect(t *testing.T) {
	h := newTestHub(t)
	h.Alias("tiny", "acme/tiny")
	c := newClient(t, h.URL, "")
	info, err := c.RepoInfo(context.Background(), Repo{Type: Model, ID: "tiny"}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "acme/tiny" {
		t.Errorf("ID = %q, want the canonical id", info.ID)
	}
}

func TestErrors(t *testing.T) {
	h := newTestHub(t)
	ctx := context.Background()
	tests := []struct {
		name  string
		token string
		call  func(c *Client) error
		want  error
	}{
		{"missing repo, anonymous (Hub answers 401)", "", func(c *Client) error {
			_, err := c.RepoInfo(ctx, Repo{Type: Model, ID: "acme/nope"}, "main")
			return err
		}, ErrRepoNotFound},
		{"missing repo, with token", "hf_x", func(c *Client) error {
			_, err := c.RepoInfo(ctx, Repo{Type: Model, ID: "acme/nope"}, "main")
			return err
		}, ErrRepoNotFound},
		{"bad revision", "", func(c *Client) error {
			_, err := c.RepoInfo(ctx, Repo{Type: Model, ID: "acme/tiny"}, "no-such-branch")
			return err
		}, ErrRevisionNotFound},
		{"missing file", "", func(c *Client) error {
			_, err := c.FileMeta(ctx, Repo{Type: Model, ID: "acme/tiny"}, "main", "nope.bin")
			return err
		}, ErrEntryNotFound},
		{"gated, anonymous", "", func(c *Client) error {
			_, err := c.Download(ctx, Repo{Type: Model, ID: "acme/gated"}, "main", "config.json", 0)
			return err
		}, ErrGated},
		{"gated, token without access", "hf_other", func(c *Client) error {
			_, err := c.Download(ctx, Repo{Type: Model, ID: "acme/gated"}, "main", "config.json", 0)
			return err
		}, ErrGated},
		{"disabled repo", "", func(c *Client) error {
			_, err := c.RepoInfo(ctx, Repo{Type: Model, ID: "acme/gone"}, "main")
			return err
		}, ErrDisabled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(newClient(t, h.URL, tt.token))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if Retryable(err) {
				t.Errorf("%v should not be retryable", err)
			}
		})
	}
}

func TestEntryNotFoundCarriesCommit(t *testing.T) {
	h := newTestHub(t)
	_, err := newClient(t, h.URL, "").FileMeta(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main", "nope.bin")
	var he *HTTPError
	if !errors.As(err, &he) || !IsCommit(he.Commit) {
		t.Fatalf("err = %#v, want an HTTPError with the commit", err)
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	h := newTestHub(t)
	h.Fail("/api/models/acme/tiny/revision", http.StatusServiceUnavailable, 2)
	c := newClient(t, h.URL, "")
	if _, err := c.RepoInfo(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main"); err != nil {
		t.Fatalf("two 503s then success: %v", err)
	}

	h.Fail("/api/models/acme/tiny/revision", http.StatusBadGateway, 10)
	_, err := c.RepoInfo(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main")
	if !errors.Is(err, ErrUnavailable) || !Retryable(err) {
		t.Fatalf("persistent 502: err = %v, want retryable ErrUnavailable", err)
	}
}

func TestTree(t *testing.T) {
	for _, pageSize := range []int{0, 1, 2} {
		h := newTestHub(t)
		h.PageSize(pageSize)
		c := newClient(t, h.URL, "")
		if _, err := c.Tree(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, strings.Repeat("0", 40)); err == nil {
			t.Fatal("unknown commit should fail")
		}
		info, _ := c.RepoInfo(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main")
		entries, err := c.Tree(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, info.SHA)
		if err != nil {
			t.Fatalf("page size %d: %v", pageSize, err)
		}
		files := map[string]TreeEntry{}
		for _, e := range entries {
			if e.IsFile() {
				files[e.Path] = e
			}
		}
		if len(files) != 3 || len(entries) != 4 {
			t.Fatalf("page size %d: got %d entries (%d files), want 4 (3)", pageSize, len(entries), len(files))
		}
		if got := files["config.json"].ETag(); got != configJSON.GitSHA1() {
			t.Errorf("config.json ETag = %s, want git sha1 %s", got, configJSON.GitSHA1())
		}
		if got := files["model.safetensors"].ETag(); got != weights.SHA256() {
			t.Errorf("model.safetensors ETag = %s, want sha256 %s", got, weights.SHA256())
		}
	}
}

func TestFileMeta(t *testing.T) {
	h := newTestHub(t)
	c := newClient(t, h.URL, "")
	repo := Repo{Type: Model, ID: "acme/tiny"}
	ctx := context.Background()

	m, err := c.FileMeta(ctx, repo, "main", "model.safetensors")
	if err != nil {
		t.Fatal(err)
	}
	if m.ETag != weights.SHA256() || m.Size != int64(len(weights.Content)) || !IsCommit(m.Commit) {
		t.Errorf("LFS meta = %+v", m)
	}
	for _, r := range h.Requests() {
		if r.Host == "cdn" {
			t.Error("FileMeta followed the redirect to the CDN")
		}
	}

	m, err = c.FileMeta(ctx, repo, "main", "config.json")
	if err != nil {
		t.Fatal(err)
	}
	if m.ETag != configJSON.GitSHA1() || m.Size != int64(len(configJSON.Content)) {
		t.Errorf("regular file meta = %+v", m)
	}

	h.OmitCommitHeader()
	if _, err := c.FileMeta(ctx, repo, "main", "config.json"); err == nil || !strings.Contains(err.Error(), "X-Repo-Commit") {
		t.Errorf("server without X-Repo-Commit: err = %v", err)
	}
}

func TestDownloadSendsTokenOnlyToHub(t *testing.T) {
	h := newTestHub(t)
	c := newClient(t, h.URL, "hf_secret")
	body, err := c.Download(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main", "model.safetensors", 0)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if string(data) != string(weights.Content) {
		t.Fatal("wrong content")
	}
	var sawHub, sawCDN bool
	for _, r := range h.Requests() {
		switch r.Host {
		case "hub":
			sawHub = true
			if r.Authorization != "Bearer hf_secret" {
				t.Errorf("hub request without token: %+v", r)
			}
		case "cdn":
			sawCDN = true
			if r.Authorization != "" {
				t.Errorf("token leaked to the CDN: %+v", r)
			}
		}
	}
	if !sawHub || !sawCDN {
		t.Errorf("expected hub and cdn requests, got %+v", h.Requests())
	}
}

func TestDownloadRange(t *testing.T) {
	h := newTestHub(t)
	c := newClient(t, h.URL, "")
	body, err := c.Download(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main", "model.safetensors", 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if body.Offset != 1000 || body.Total != int64(len(weights.Content)) {
		t.Errorf("Offset=%d Total=%d", body.Offset, body.Total)
	}
	data, _ := io.ReadAll(body)
	if len(data) != len(weights.Content)-1000 {
		t.Errorf("read %d bytes, want %d", len(data), len(weights.Content)-1000)
	}
	var cdnRange string
	for _, r := range h.Requests() {
		if r.Host == "cdn" {
			cdnRange = r.Range
		}
	}
	if cdnRange != "bytes=1000-" {
		t.Errorf("Range on the CDN request = %q; it must survive the redirect", cdnRange)
	}
}

func TestDownloadSmallFileViaRelativeRedirect(t *testing.T) {
	h := newTestHub(t)
	c := newClient(t, h.URL, "")
	body, err := c.Download(context.Background(), Repo{Type: Model, ID: "acme/tiny"}, "main", "config.json", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != string(configJSON.Content) {
		t.Errorf("got %q", data)
	}
}

func TestParseRepo(t *testing.T) {
	good := map[string]Repo{
		"acme/tiny":          {Model, "acme/tiny"},
		"gpt2":               {Model, "gpt2"},
		"datasets/acme/data": {Dataset, "acme/data"},
		"spaces/acme/app":    {Space, "acme/app"},
		"Org-1/Name_2.v3":    {Model, "Org-1/Name_2.v3"},
	}
	for in, want := range good {
		got, err := ParseRepo(in)
		if err != nil || got != want {
			t.Errorf("ParseRepo(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a/b/c", "../etc", "acme/..", "acme/.hidden", "acme/", "/acme", `acme\tiny`, "acme/ti ny"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Errorf("ParseRepo(%q) accepted", bad)
		}
	}
}

func TestValidatePath(t *testing.T) {
	for _, ok := range []string{"config.json", "onnx/model.onnx", "a/b/c.bin", ".gitattributes"} {
		if err := ValidatePath(ok); err != nil {
			t.Errorf("ValidatePath(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/../../x", "a//b", `a\b`, "a/./b", "x\x00y"} {
		if err := ValidatePath(bad); err == nil {
			t.Errorf("ValidatePath(%q) accepted", bad)
		}
	}
}

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		in           string
		start, total int64
		ok           bool
	}{
		{"bytes 100-199/1000", 100, 1000, true},
		{"bytes 0-0/*", 0, -1, true},
		{"bytes */1000", 0, 0, false},
		{"items 1-2/3", 0, 0, false},
	}
	for _, tt := range tests {
		start, total, ok := parseContentRange(tt.in)
		if start != tt.start || total != tt.total || ok != tt.ok {
			t.Errorf("parseContentRange(%q) = %d, %d, %v", tt.in, start, total, ok)
		}
	}
}

// TestRealHub checks our assumptions against huggingface.co itself.
func TestRealHub(t *testing.T) {
	testutil.Network(t)
	c, err := New("https://huggingface.co", Options{UserAgent: "weightkeep/test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := Repo{Type: Model, ID: "prajjwal1/bert-tiny"}
	info, err := c.RepoInfo(ctx, repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := c.Tree(ctx, repo, info.SHA)
	if err != nil {
		t.Fatal(err)
	}
	var sawLFS bool
	for _, e := range entries {
		if !e.IsFile() {
			continue
		}
		m, err := c.FileMeta(ctx, repo, info.SHA, e.Path)
		if err != nil {
			t.Fatalf("%s: %v", e.Path, err)
		}
		if m.ETag != e.ETag() || m.Commit != info.SHA {
			t.Errorf("%s: HEAD says etag %s commit %s; tree says %s commit %s", e.Path, m.ETag, m.Commit, e.ETag(), info.SHA)
		}
		if e.LFS != nil {
			sawLFS = true
			body, err := c.Download(ctx, repo, info.SHA, e.Path, e.LFS.Size-100)
			if err != nil {
				t.Fatalf("ranged download of %s: %v", e.Path, err)
			}
			n, _ := io.Copy(io.Discard, body)
			body.Close()
			if n != 100 || body.Offset != e.LFS.Size-100 {
				t.Errorf("%s: read %d bytes from offset %d", e.Path, n, body.Offset)
			}
		}
	}
	if !sawLFS {
		t.Error("expected at least one LFS file in prajjwal1/bert-tiny")
	}
}
