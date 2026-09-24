// Package fakehub is an in-memory imitation of the Hugging Face Hub for
// tests. It reproduces the responses weightkeep depends on, as recorded from
// huggingface.co in September 2026: relative 307s for small files and legacy
// ids, 302s to a separate CDN host for LFS files, Xet headers, error codes,
// and Link pagination. It can also inject failures.
package fakehub

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: git blob ids are SHA-1 by definition
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// File is a file in a fake repo.
type File struct {
	Path    string
	Content []byte
	LFS     bool
}

// SHA256 returns the hex SHA-256 of the content.
func (f File) SHA256() string {
	s := sha256.Sum256(f.Content)
	return hex.EncodeToString(s[:])
}

// GitSHA1 returns the git blob id of the content.
func (f File) GitSHA1() string {
	h := sha1.New() //nolint:gosec // G401: git blob id
	fmt.Fprintf(h, "blob %d\x00", len(f.Content))
	h.Write(f.Content)
	return hex.EncodeToString(h.Sum(nil))
}

// pointer is the LFS pointer file git stores in place of the content.
func (f File) pointer() File {
	return File{Content: []byte(fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", f.SHA256(), len(f.Content)))}
}

// ETag is what the Hub serves as the file's ETag.
func (f File) ETag() string {
	if f.LFS {
		return f.SHA256()
	}
	return f.GitSHA1()
}

// Repo is a fake model repo with one commit.
type Repo struct {
	ID       string
	Commit   string            // 40 hex; generated from ID if empty
	Branches map[string]string // name -> commit; "main" is added automatically
	Files    []File
	Gated    string // "", "auto" or "manual"
	License  string
	// BaseModel is cardData.base_model.
	BaseModel string
	Disabled  bool
	// Allowed tokens for gated repos.
	Tokens []string
}

// Hub is a running fake Hub. Use URL as the Hub base.
type Hub struct {
	URL string // the Hub
	CDN string // the separate CDN host LFS files redirect to

	hub, cdn *httptest.Server

	mu       sync.Mutex
	repos    map[string]*Repo
	aliases  map[string]string
	blobs    map[string][]byte // sha256 -> content, served by the CDN
	pageSize int
	failures []failure
	cutAfter int64 // if > 0, CDN bodies stop after this many bytes, once
	requests []Request
	noCommit bool
}

// Request is a logged request.
type Request struct {
	Host          string // "hub" or "cdn"
	Method        string
	Path          string
	Range         string
	Authorization string
}

type failure struct {
	match  string
	status int
	n      int
}

// New starts a fake Hub with the given repos.
func New(repos ...*Repo) *Hub {
	h := &Hub{repos: map[string]*Repo{}, aliases: map[string]string{}, blobs: map[string][]byte{}}
	h.hub = httptest.NewServer(http.HandlerFunc(h.serveHub))
	h.cdn = httptest.NewServer(http.HandlerFunc(h.serveCDN))
	h.URL, h.CDN = h.hub.URL, h.cdn.URL
	for _, r := range repos {
		h.Add(r)
	}
	return h
}

// Close stops both servers.
func (h *Hub) Close() {
	h.hub.Close()
	h.cdn.Close()
}

// Add adds or replaces a repo.
func (h *Hub) Add(r *Repo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Commit == "" {
		s := sha1.Sum([]byte(r.ID)) //nolint:gosec // G401: fake commit id
		r.Commit = hex.EncodeToString(s[:])
	}
	if r.Branches == nil {
		r.Branches = map[string]string{}
	}
	if _, ok := r.Branches["main"]; !ok {
		r.Branches["main"] = r.Commit
	}
	h.repos[r.ID] = r
	for _, f := range r.Files {
		if f.LFS {
			h.blobs[f.SHA256()] = f.Content
		}
	}
}

// Remove deletes a repo, as if its owner deleted it.
func (h *Hub) Remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.repos, id)
}

// Alias makes old redirect to new with a relative 307, like legacy ids.
func (h *Hub) Alias(old, new string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.aliases[old] = new
}

// PageSize paginates tree listings.
func (h *Hub) PageSize(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pageSize = n
}

// Fail makes the next n requests whose path contains match return status.
func (h *Hub) Fail(match string, status, n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failures = append(h.failures, failure{match, status, n})
}

// CutNextDownload makes the next CDN response stop after n bytes, as if the
// connection dropped.
func (h *Hub) CutNextDownload(n int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cutAfter = n
}

// OmitCommitHeader makes resolve responses leave out X-Repo-Commit, like a
// server that is not really a Hub.
func (h *Hub) OmitCommitHeader() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.noCommit = true
}

// Requests returns the requests seen so far.
func (h *Hub) Requests() []Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Request(nil), h.requests...)
}

// ResetRequests clears the request log.
func (h *Hub) ResetRequests() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = nil
}

func (h *Hub) log(host string, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, Request{
		Host: host, Method: r.Method, Path: r.URL.EscapedPath(),
		Range: r.Header.Get("Range"), Authorization: r.Header.Get("Authorization"),
	})
}

func (h *Hub) injected(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.failures {
		f := &h.failures[i]
		if f.n > 0 && strings.Contains(path, f.match) {
			f.n--
			return f.status
		}
	}
	return 0
}

func (h *Hub) serveHub(w http.ResponseWriter, r *http.Request) {
	h.log("hub", r)
	if status := h.injected(r.URL.EscapedPath()); status != 0 {
		http.Error(w, "injected failure", status)
		return
	}
	path := r.URL.EscapedPath()
	switch {
	case strings.HasPrefix(path, "/v2/"):
		h.serveOllama(w, r, strings.TrimPrefix(path, "/v2/"))
	case strings.HasPrefix(path, "/api/models/"):
		h.serveAPI(w, r, strings.TrimPrefix(path, "/api/models/"))
	case strings.HasPrefix(path, "/api/resolve-cache/models/"):
		h.serveResolveCache(w, r, strings.TrimPrefix(path, "/api/resolve-cache/models/"))
	case strings.Contains(path, "/resolve/"):
		h.serveResolve(w, r, path[1:])
	default:
		http.NotFound(w, r)
	}
}

// lookup finds a repo by id and applies the Hub's access rules. It writes the
// error response itself and returns nil when access is refused.
func (h *Hub) lookup(w http.ResponseWriter, r *http.Request, id, rest string, isAPI bool) *Repo {
	h.mu.Lock()
	repo := h.repos[id]
	alias, aliased := h.aliases[id]
	h.mu.Unlock()

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if aliased {
		prefix := "/"
		if isAPI {
			prefix = "/api/models/"
		}
		w.Header().Set("Location", prefix+alias+rest)
		w.WriteHeader(http.StatusTemporaryRedirect)
		return nil
	}
	if repo == nil {
		if token == "" {
			w.Header().Set("X-Error-Message", "Invalid username or password.")
			http.Error(w, `{"error":"Invalid username or password."}`, http.StatusUnauthorized)
			return nil
		}
		w.Header().Set("X-Error-Code", "RepoNotFound")
		w.Header().Set("X-Error-Message", "Repository not found")
		http.Error(w, `{"error":"Repository not found"}`, http.StatusNotFound)
		return nil
	}
	if repo.Disabled {
		w.Header().Set("X-Error-Message", "Access to this resource is disabled.")
		http.Error(w, `{"error":"Access to this resource is disabled."}`, http.StatusForbidden)
		return nil
	}
	return repo
}

func (h *Hub) gateCheck(w http.ResponseWriter, r *http.Request, repo *Repo) bool {
	if repo.Gated == "" {
		return true
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	for _, t := range repo.Tokens {
		if t == token {
			return true
		}
	}
	w.Header().Set("X-Error-Code", "GatedRepo")
	w.Header().Set("X-Error-Message", "Access to model "+repo.ID+" is restricted.")
	status := http.StatusForbidden
	if token == "" {
		status = http.StatusUnauthorized
	}
	http.Error(w, "Access to model "+repo.ID+" is restricted.", status)
	return false
}

func (repo *Repo) resolveRev(rev string) (string, bool) {
	if c, ok := repo.Branches[rev]; ok {
		return c, true
	}
	if rev == repo.Commit || (len(rev) >= 7 && strings.HasPrefix(repo.Commit, rev)) {
		return repo.Commit, true
	}
	for _, c := range repo.Branches {
		if c == rev {
			return c, true
		}
	}
	return "", false
}

func revisionNotFound(w http.ResponseWriter, rev string) {
	w.Header().Set("X-Error-Code", "RevisionNotFound")
	w.Header().Set("X-Error-Message", "Invalid rev id: "+rev)
	http.Error(w, `{"error":"Invalid rev id: `+rev+`"}`, http.StatusNotFound)
}

// splitID takes "org/name/<rest>" or "name/<rest>" where rest starts with one
// of the markers, and returns the id and "/<rest>".
func (h *Hub) splitID(path string, markers ...string) (id, rest string) {
	for _, m := range markers {
		if i := strings.Index(path, m); i >= 0 {
			return path[:i], path[i:]
		}
	}
	return path, ""
}

func (h *Hub) serveAPI(w http.ResponseWriter, r *http.Request, path string) {
	id, rest := h.splitID(path, "/revision/", "/tree/", "/refs")
	repo := h.lookup(w, r, id, rest, true)
	if repo == nil {
		return
	}
	switch {
	case rest == "" || strings.HasPrefix(rest, "/revision/"):
		rev := "main"
		if rest != "" {
			rev, _ = url.PathUnescape(strings.TrimPrefix(rest, "/revision/"))
		}
		commit, ok := repo.resolveRev(rev)
		if !ok {
			revisionNotFound(w, rev)
			return
		}
		h.writeInfo(w, repo, commit)
	case strings.HasPrefix(rest, "/tree/"):
		revPath := strings.TrimPrefix(rest, "/tree/")
		revEsc, _, _ := strings.Cut(revPath, "/")
		rev, _ := url.PathUnescape(revEsc)
		if _, ok := repo.resolveRev(rev); !ok {
			revisionNotFound(w, rev)
			return
		}
		h.writeTree(w, r, repo)
	case rest == "/refs":
		type branch struct {
			Name         string `json:"name"`
			Ref          string `json:"ref"`
			TargetCommit string `json:"targetCommit"`
		}
		var bs []branch
		for name, c := range repo.Branches {
			bs = append(bs, branch{name, "refs/heads/" + name, c})
		}
		sort.Slice(bs, func(i, j int) bool { return bs[i].Name < bs[j].Name })
		writeJSON(w, map[string]any{"branches": bs, "tags": []any{}, "converts": []any{}})
	default:
		http.NotFound(w, r)
	}
}

func (h *Hub) writeInfo(w http.ResponseWriter, repo *Repo, commit string) {
	siblings := []map[string]string{}
	for _, f := range repo.Files {
		siblings = append(siblings, map[string]string{"rfilename": f.Path})
	}
	var gated any = false
	if repo.Gated != "" {
		gated = repo.Gated
	}
	info := map[string]any{
		"_id":          "0123456789abcdef01234567",
		"id":           repo.ID,
		"modelId":      repo.ID,
		"sha":          commit,
		"private":      false,
		"gated":        gated,
		"disabled":     false,
		"lastModified": time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"siblings":     siblings,
		"tags":         []string{"safetensors"},
	}
	card := map[string]any{}
	if repo.License != "" {
		card["license"] = repo.License
	}
	if repo.BaseModel != "" {
		card["base_model"] = repo.BaseModel
	}
	if len(card) > 0 {
		info["cardData"] = card
	}
	writeJSON(w, info)
}

func (h *Hub) writeTree(w http.ResponseWriter, r *http.Request, repo *Repo) {
	type lfs struct {
		OID         string `json:"oid"`
		Size        int    `json:"size"`
		PointerSize int    `json:"pointerSize"`
	}
	type entry struct {
		Type    string `json:"type"`
		OID     string `json:"oid"`
		Size    int    `json:"size"`
		LFS     *lfs   `json:"lfs,omitempty"`
		XetHash string `json:"xetHash,omitempty"`
		Path    string `json:"path"`
	}
	var entries []entry
	dirs := map[string]bool{}
	for _, f := range repo.Files {
		for d := f.Path; strings.Contains(d, "/"); {
			d = d[:strings.LastIndex(d, "/")]
			if !dirs[d] {
				dirs[d] = true
				entries = append(entries, entry{Type: "directory", OID: strings.Repeat("d", 40), Path: d})
			}
		}
		e := entry{Type: "file", OID: f.GitSHA1(), Size: len(f.Content), Path: f.Path}
		if f.LFS {
			p := f.pointer()
			e.OID = p.GitSHA1()
			e.LFS = &lfs{OID: f.SHA256(), Size: len(f.Content), PointerSize: len(p.Content)}
			e.XetHash = strings.Repeat("7", 64)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	h.mu.Lock()
	size := h.pageSize
	h.mu.Unlock()
	if size > 0 {
		start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		end := min(start+size, len(entries))
		if end < len(entries) {
			next := *r.URL
			q := next.Query()
			q.Set("cursor", strconv.Itoa(end))
			next.RawQuery = q.Encode()
			w.Header().Set("Link", fmt.Sprintf(`<%s%s>; rel="next"`, h.URL, next.RequestURI()))
		}
		entries = entries[start:end]
	}
	if entries == nil {
		entries = []entry{}
	}
	writeJSON(w, entries)
}

func (h *Hub) serveResolve(w http.ResponseWriter, r *http.Request, path string) {
	id, rest := h.splitID(path, "/resolve/")
	repo := h.lookup(w, r, id, rest, false)
	if repo == nil {
		return
	}
	revEsc, filePathEsc, _ := strings.Cut(strings.TrimPrefix(rest, "/resolve/"), "/")
	rev, _ := url.PathUnescape(revEsc)
	filePath, _ := url.PathUnescape(filePathEsc)
	if !h.gateCheck(w, r, repo) {
		return
	}
	commit, ok := repo.resolveRev(rev)
	if !ok {
		revisionNotFound(w, rev)
		return
	}
	h.mu.Lock()
	noCommit := h.noCommit
	h.mu.Unlock()
	if !noCommit {
		w.Header().Set("X-Repo-Commit", commit)
	}
	var file *File
	for i := range repo.Files {
		if repo.Files[i].Path == filePath {
			file = &repo.Files[i]
		}
	}
	if file == nil {
		w.Header().Set("X-Error-Code", "EntryNotFound")
		w.Header().Set("X-Error-Message", "Entry not found")
		http.Error(w, "Entry not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Linked-Etag", `"`+file.ETag()+`"`)
	if file.LFS {
		w.Header().Set("X-Linked-Size", strconv.Itoa(len(file.Content)))
		w.Header().Set("X-Xet-Hash", strings.Repeat("7", 64))
		w.Header().Set("Link", `<`+h.URL+`/api/models/`+repo.ID+`/xet-read-token/`+commit+`>; rel="xet-auth"`)
		w.Header().Set("Location", h.CDN+"/xet-bridge/"+file.SHA256()+"?Expires=9999999999&Signature=secret")
		w.WriteHeader(http.StatusFound)
		return
	}
	w.Header().Set("Location", "/api/resolve-cache/models/"+repo.ID+"/"+commit+"/"+filePathEsc)
	w.WriteHeader(http.StatusTemporaryRedirect)
}

func (h *Hub) serveResolveCache(w http.ResponseWriter, r *http.Request, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, repo := range h.repos {
		prefix := id + "/" + repo.Commit + "/"
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		p, _ := url.PathUnescape(strings.TrimPrefix(path, prefix))
		for _, f := range repo.Files {
			if f.Path == p && !f.LFS {
				w.Header().Set("ETag", `"`+f.GitSHA1()+`"`)
				if !h.noCommit {
					w.Header().Set("X-Repo-Commit", repo.Commit)
				}
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.Content))
				return
			}
		}
	}
	http.NotFound(w, r)
}

func (h *Hub) serveCDN(w http.ResponseWriter, r *http.Request) {
	h.log("cdn", r)
	if status := h.injected("cdn:" + r.URL.Path); status != 0 {
		http.Error(w, "injected failure", status)
		return
	}
	sum := strings.TrimPrefix(r.URL.Path, "/xet-bridge/")
	h.mu.Lock()
	content, ok := h.blobs[sum]
	cut := h.cutAfter
	h.cutAfter = 0
	h.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if cut > 0 {
		w = &cutWriter{ResponseWriter: w, left: cut}
	}
	w.Header().Set("ETag", `"`+strings.Repeat("7", 64)+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
}

// cutWriter stops writing after left bytes and then aborts the connection.
type cutWriter struct {
	http.ResponseWriter
	left int64
}

func (c *cutWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > c.left {
		b = b[:c.left]
	}
	n, err := c.ResponseWriter.Write(b)
	c.left -= int64(n)
	if c.left <= 0 {
		if f, ok := c.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}
	return n, err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// ollamaConfig is the generated config blob for a repo's Ollama manifest.
func ollamaConfig(repo *Repo) []byte {
	return []byte(`{"model_format":"gguf","repo":"` + repo.ID + `"}`)
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// serveOllama imitates the Hub's Ollama registry: /v2/{id}/manifests/{tag}
// picks the GGUF whose name contains the tag; the config layer is generated
// and served with 200 plus a Location header, the model layer with a 307
// to the resolve URL.
func (h *Hub) serveOllama(w http.ResponseWriter, r *http.Request, rest string) {
	var id, kind, arg string
	if i := strings.Index(rest, "/manifests/"); i > 0 {
		id, kind, arg = rest[:i], "manifest", rest[i+len("/manifests/"):]
	} else if i := strings.Index(rest, "/blobs/sha256:"); i > 0 {
		id, kind, arg = rest[:i], "blob", rest[i+len("/blobs/sha256:"):]
	} else {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	repo := h.repos[id]
	h.mu.Unlock()
	if repo == nil {
		http.NotFound(w, r)
		return
	}
	var gguf *File
	for i := range repo.Files {
		f := &repo.Files[i]
		if strings.HasSuffix(f.Path, ".gguf") && (kind == "blob" && f.SHA256() == arg || kind == "manifest" && strings.Contains(f.Path, arg)) {
			gguf = f
			break
		}
	}
	cfg := ollamaConfig(repo)
	switch {
	case kind == "manifest" && gguf != nil:
		writeJSON(w, map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
			"config":        map[string]any{"digest": "sha256:" + sha256hex(cfg), "mediaType": "application/vnd.docker.container.image.v1+json", "size": len(cfg)},
			"layers": []map[string]any{
				{"digest": "sha256:" + gguf.SHA256(), "mediaType": "application/vnd.ollama.image.model", "size": len(gguf.Content)},
			},
		})
	case kind == "blob" && arg == sha256hex(cfg):
		w.Header().Set("Location", "?__sign=fake")
		w.Header().Set("Content-Length", strconv.Itoa(len(cfg)))
		_, _ = w.Write(cfg)
	case kind == "blob" && gguf != nil:
		w.Header().Set("Location", "/"+repo.ID+"/resolve/main/"+gguf.Path)
		w.WriteHeader(http.StatusTemporaryRedirect)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"The specified tag is not a valid quantization scheme."}`))
	}
}
