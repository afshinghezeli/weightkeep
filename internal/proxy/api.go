package proxy

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: synthetic git tree ids, not security
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.kind {
	case routeInfo:
		s.handleInfo(w, r, rt)
	case routeTree:
		s.handleTree(w, r, rt)
	case routePathsInfo:
		s.handlePathsInfo(w, r, rt)
	case routeRefs:
		s.handleRefs(w, r, rt)
	case routeAPIOther:
		s.passthrough(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleInfo answers /api/{type}s/{id}[/revision/{rev}]. It replays the
// upstream's answer saved when the revision was learned, with siblings
// rebuilt from the manifest, or a minimal synthesised one.
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request, rt route) {
	m, err := s.revision(r.Context(), rt.repo, rt.rev)
	if err != nil {
		writeError(w, err)
		return
	}
	info := map[string]any{}
	if raw, err := manifest.LoadInfo(s.k.Store, m.Repo, m.Commit); err == nil {
		if err := json.Unmarshal(raw, &info); err != nil {
			info = map[string]any{}
		}
	}
	info["id"] = m.Repo.ID
	info["modelId"] = m.Repo.ID
	info["sha"] = m.Commit
	if _, ok := info["private"]; !ok {
		info["private"] = false
	}
	if _, ok := info["gated"]; !ok {
		info["gated"] = gatedValue(m.License.Gated)
	}
	if _, ok := info["disabled"]; !ok {
		info["disabled"] = false
	}
	if _, ok := info["cardData"]; !ok && len(m.License.IDs) > 0 {
		info["cardData"] = map[string]any{"license": m.License.IDs[0]}
	}
	withBlobs := r.URL.Query().Get("blobs") == "true"
	siblings := make([]map[string]any, 0, len(m.Files))
	for _, f := range m.Files {
		sib := map[string]any{"rfilename": f.Path}
		if withBlobs {
			sib["blobId"] = f.GitSHA1
			sib["size"] = f.Size
			if f.LFS {
				sib["lfs"] = map[string]any{"sha256": f.SHA256, "size": f.Size, "pointerSize": pointerSize(f)}
			}
		}
		siblings = append(siblings, sib)
	}
	info["siblings"] = siblings
	s.writeJSON(w, r, info)
}

func gatedValue(g string) any {
	if g == "" {
		return false
	}
	return g
}

// treeEntry mirrors the Hub's tree/paths-info entries, without xetHash.
type treeEntry struct {
	Type string    `json:"type"`
	OID  string    `json:"oid"`
	Size int64     `json:"size"`
	LFS  *lfsEntry `json:"lfs,omitempty"`
	Path string    `json:"path"`
}

type lfsEntry struct {
	OID         string `json:"oid"`
	Size        int64  `json:"size"`
	PointerSize int    `json:"pointerSize"`
}

// entries lists every file and directory of the revision, sorted by path.
func entries(m *manifest.Manifest) []treeEntry {
	out := make([]treeEntry, 0, len(m.Files))
	dirs := map[string]bool{}
	for _, f := range m.Files {
		for d := f.Path; strings.Contains(d, "/"); {
			d = d[:strings.LastIndex(d, "/")]
			if dirs[d] {
				break
			}
			dirs[d] = true
			out = append(out, treeEntry{Type: "directory", OID: treeOID(m.Commit, d), Path: d})
		}
		e := treeEntry{Type: "file", OID: f.GitSHA1, Size: f.Size, Path: f.Path}
		if f.LFS {
			e.LFS = &lfsEntry{OID: f.SHA256, Size: f.Size, PointerSize: pointerSize(f)}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// treeOID is a stable stand-in for a directory's git tree id, which the
// manifest doesn't record. Clients only use it as an opaque value.
func treeOID(commit, dir string) string {
	h := sha1.Sum([]byte("tree " + commit + " " + dir)) //nolint:gosec // G401: opaque id
	return hex.EncodeToString(h[:])
}

// pointerSize is the length of the git-lfs pointer file for f, which is
// fully determined by its SHA-256 and size.
func pointerSize(f manifest.File) int {
	return len(fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", f.SHA256, f.Size))
}

// handleTree answers /api/{type}s/{id}/tree/{rev}[/{path}] from the
// manifest. Without a limit it returns everything in one page, which is
// what llama.cpp (no pagination support) needs. With limit or cursor it
// pages and understands the cursor text-generation-webui builds by hand.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request, rt route) {
	m, err := s.revision(r.Context(), rt.repo, rt.rev)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	recursive := q.Get("recursive") == "true" || q.Get("recursive") == "1"
	all := entries(m)

	prefix := ""
	if rt.path != "" {
		prefix = rt.path + "/"
		isDir := false
		for _, e := range all {
			if e.Path == rt.path && e.Type == "directory" {
				isDir = true
			}
		}
		if !isDir {
			w.Header().Set("X-Error-Code", "EntryNotFound")
			http.Error(w, "Entry not found", http.StatusNotFound)
			return
		}
	}
	var list []treeEntry
	for _, e := range all {
		rest, ok := strings.CutPrefix(e.Path, prefix)
		if !ok || rest == "" {
			continue
		}
		if !recursive && strings.Contains(rest, "/") {
			continue
		}
		list = append(list, e)
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	after, hasCursor := decodeCursor(q.Get("cursor"))
	if hasCursor {
		i := sort.Search(len(list), func(i int) bool { return list[i].Path > after })
		list = list[i:]
	}
	if limit > 0 && len(list) > limit {
		list = list[:limit]
		next := *r.URL
		nq := next.Query()
		nq.Set("cursor", encodeCursor(list[len(list)-1].Path, limit))
		next.RawQuery = nq.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<%s%s>; rel="next"`, baseURL(r), next.RequestURI()))
	}
	if list == nil {
		list = []treeEntry{}
	}
	s.writeJSON(w, r, list)
}

// Hub cursors are base64(base64(json{"file_name": ...}) + ":" + limit).
func encodeCursor(after string, limit int) string {
	inner, _ := json.Marshal(map[string]string{"file_name": after})
	return base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(inner) + ":" + strconv.Itoa(limit)))
}

func decodeCursor(c string) (string, bool) {
	if c == "" {
		return "", false
	}
	outer, err := base64.StdEncoding.DecodeString(c)
	if err != nil {
		return "", false
	}
	innerB64, _, _ := strings.Cut(string(outer), ":")
	inner, err := base64.StdEncoding.DecodeString(innerB64)
	if err != nil {
		return "", false
	}
	var v struct {
		FileName string `json:"file_name"`
	}
	if json.Unmarshal(inner, &v) != nil {
		return "", false
	}
	return v.FileName, true
}

// handlePathsInfo answers POST /api/{type}s/{id}/paths-info/{rev}. Paths
// come as form values (huggingface_hub) or JSON; unknown paths are omitted.
func (s *Server) handlePathsInfo(w http.ResponseWriter, r *http.Request, rt route) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var paths []string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Paths []string `json:"paths"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "bad JSON body", http.StatusBadRequest)
			return
		}
		paths = body.Paths
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form body", http.StatusBadRequest)
			return
		}
		paths = r.PostForm["paths"]
	}
	m, err := s.revision(r.Context(), rt.repo, rt.rev)
	if err != nil {
		writeError(w, err)
		return
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[strings.TrimSuffix(p, "/")] = true
	}
	out := []treeEntry{}
	for _, e := range entries(m) {
		if want[e.Path] {
			out = append(out, e)
		}
	}
	s.writeJSON(w, r, out)
}

// handleRefs relays the upstream's refs when it can, else answers from the
// refs weightkeep has seen.
func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request, rt route) {
	if !s.offline {
		resp, err := s.k.Hub.Passthrough(r.Context(), http.MethodGet, r.URL.RequestURI(), nil, r.Header)
		if err == nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			s.relay(w, r, resp)
			return
		}
		if err == nil {
			resp.Body.Close()
		}
	}
	mrepo := manifest.Repo{Type: string(rt.repo.Type), ID: rt.repo.ID}
	commits, err := manifest.Revisions(r.Context(), s.k.Store, mrepo)
	if err != nil || len(commits) == 0 {
		writeError(w, manifest.ErrNotFound)
		return
	}
	type branch struct {
		Name         string `json:"name"`
		Ref          string `json:"ref"`
		TargetCommit string `json:"targetCommit"`
	}
	branches := []branch{}
	for _, c := range commits {
		names, err := manifest.RefsFor(r.Context(), s.k.Store, mrepo, c)
		if err != nil {
			writeError(w, err)
			return
		}
		for _, n := range names {
			if !strings.HasPrefix(n, "refs/") && !hub.IsCommit(n) {
				branches = append(branches, branch{n, "refs/heads/" + n, c})
			}
		}
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Name < branches[j].Name })
	s.writeJSON(w, r, map[string]any{"branches": branches, "tags": []any{}, "converts": []any{}})
}

// passthrough relays any other /api/ request upstream, or answers offline.
func (s *Server) passthrough(w http.ResponseWriter, r *http.Request) {
	if s.offline {
		if r.URL.Path == "/api/whoami-v2" {
			w.Header().Set("X-Error-Message", "Invalid username or password.")
			http.Error(w, `{"error":"Invalid username or password."}`, http.StatusUnauthorized)
			return
		}
		writeUnavailable(w, "weightkeep is running offline")
		return
	}
	var body io.Reader
	if r.Body != nil {
		body = http.MaxBytesReader(w, r.Body, 32<<20)
	}
	resp, err := s.k.Hub.Passthrough(r.Context(), r.Method, r.URL.RequestURI(), body, r.Header)
	if err != nil {
		writeError(w, err)
		return
	}
	s.relay(w, r, resp)
}

// relay copies an upstream response, removing Xet signals and pointing
// absolute upstream URLs at this server.
func (s *Server) relay(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	defer resp.Body.Close()
	upstream := s.k.Hub.Base()
	self := baseURL(r)
	h := w.Header()
	for _, k := range []string{"Content-Type", "X-Error-Code", "X-Error-Message", "X-Repo-Commit", "ETag", "WWW-Authenticate"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		h.Set("Location", strings.ReplaceAll(loc, upstream, self))
	}
	if link := stripXetLinks(resp.Header.Get("Link")); link != "" {
		h.Set("Link", strings.ReplaceAll(link, upstream, self))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		writeUnavailable(w, "reading upstream response: "+err.Error())
		return
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") {
		data = stripXetJSON(data)
	}
	data = bytes.ReplaceAll(data, []byte(upstream), []byte(self))
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.k.Hub != nil {
		data = bytes.ReplaceAll(data, []byte(s.k.Hub.Base()), []byte(baseURL(r)))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// stripXetJSON removes every "xetHash" key. Newer huggingface_hub skips its
// HEAD request and goes straight to Xet storage when tree data has one.
func stripXetJSON(data []byte) []byte {
	if !bytes.Contains(data, []byte("xetHash")) {
		return data
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return data
	}
	out, err := json.Marshal(dropKey(v, "xetHash"))
	if err != nil {
		return data
	}
	return out
}

func dropKey(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, key)
		for k, x := range t {
			t[k] = dropKey(x, key)
		}
	case []any:
		for i, x := range t {
			t[i] = dropKey(x, key)
		}
	}
	return v
}

// stripXetLinks drops xet-auth and xet-reconstruction-info from a Link
// header and keeps the rest (pagination).
func stripXetLinks(h string) string {
	if h == "" {
		return ""
	}
	var keep []string
	for _, part := range strings.Split(h, ",") {
		if strings.Contains(part, "xet-") {
			continue
		}
		keep = append(keep, strings.TrimSpace(part))
	}
	return strings.Join(keep, ", ")
}

// baseURL is how the client reached us, for rewriting absolute URLs.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "http" || p == "https" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}
