package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request, rt route) {
	m, err := s.revision(r.Context(), rt.repo, rt.rev)
	if err != nil {
		writeError(w, err)
		return
	}
	h := w.Header()
	h.Set("X-Repo-Commit", m.Commit)
	f, ok := m.File(rt.path)
	if !ok {
		h.Set("X-Error-Code", "EntryNotFound")
		h.Set("X-Error-Message", "Entry not found")
		http.Error(w, "Entry not found", http.StatusNotFound)
		return
	}

	etag := `"` + f.ETag() + `"`
	h.Set("ETag", etag)
	h.Set("X-Linked-Etag", etag)
	h.Set("X-Linked-Size", strconv.FormatInt(f.Size, 10))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": path.Base(f.Path)}))
	h.Set("Cache-Control", "no-store")

	if f.Size == 0 {
		// No Range handling for empty files: "bytes=0-" on a zero-length
		// body would be a 416, which breaks snapshot_download (olah #58).
		h.Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodHead {
		// Answered from the manifest, without touching the blob: HEAD must
		// be fast even for files we haven't fetched yet.
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	if f.SHA256 != "" && s.k.Store.Has(f.SHA256) {
		s.serveBlob(w, r, f)
		return
	}
	if s.offline {
		writeUnavailable(w, fmt.Sprintf("%s is not kept locally and weightkeep is running offline", f.Path))
		return
	}
	target := fetch.Target{
		Repo: rt.repo, Commit: m.Commit, Path: f.Path, Size: f.Size,
		TreeOID: f.GitSHA1, LFS: f.LFS, SHA256: f.SHA256,
	}
	if rt.repo.ID != m.Repo.ID {
		target.Repo.ID = m.Repo.ID // canonical id
	}
	s.serveFlight(w, r, target, s.k.ClientFor(m.Upstream))
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, f manifest.File) {
	file, err := s.k.Store.Open(f.SHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	// ServeContent handles Range, If-Range and If-None-Match. The ETag set
	// above makes its conditional logic match what the Hub does.
	http.ServeContent(w, r, "", time.Time{}, file) // zero time: no Last-Modified
}

// flight is one blob being fetched, shared by every request that wants it.
type flight struct {
	sha256 string
	part   string // path of the partial file while downloading
	blob   string // path of the blob once committed

	mu      sync.Mutex
	written int64
	done    bool
	err     error
	changed chan struct{}
}

type flights struct {
	mu sync.Mutex
	m  map[string]*flight
}

func newFlights() *flights { return &flights{m: map[string]*flight{}} }

type flightState struct {
	written int64
	done    bool
	err     error
	changed <-chan struct{} // closed on the next update
}

func (f *flight) state() flightState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return flightState{f.written, f.done, f.err, f.changed}
}

func (f *flight) update(written int64, done bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if written >= 0 {
		f.written = written
	}
	if done {
		f.done, f.err = true, err
	}
	close(f.changed)
	f.changed = make(chan struct{})
}

// join returns the flight for t, starting the download from client if
// nobody has.
func (s *Server) join(t fetch.Target, client *hub.Client) *flight {
	fl := s.flights
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if f, ok := fl.m[t.SHA256]; ok {
		return f
	}
	f := &flight{
		sha256:  t.SHA256,
		part:    s.k.Store.PartialPath(t.SHA256),
		blob:    s.k.Store.Path(t.SHA256),
		changed: make(chan struct{}),
	}
	fl.m[t.SHA256] = f
	fetcher := *s.k.Fetcher
	fetcher.Hub = client
	fetcher.Events = func(e fetch.Event) {
		switch e.State {
		case fetch.Started, fetch.Progress, fetch.Retrying:
			f.update(e.Done, false, nil)
		}
	}
	go func() {
		// The download outlives the request that started it; a client that
		// disconnects shouldn't waste what was already transferred.
		_, err := fetcher.File(context.Background(), t)
		if err != nil {
			s.log.Error("fetch failed", "repo", t.Repo.String(), "path", t.Path, "err", err)
		}
		f.update(t.Size, true, err)
		fl.mu.Lock()
		delete(fl.m, t.SHA256)
		fl.mu.Unlock()
	}()
	return f
}

// serveFlight streams a file while it downloads. Bytes are sent as they
// arrive, except the last one, which is held back until the fetcher has
// verified the whole file. A client therefore never ends up with a complete
// file whose hash is wrong: if verification fails the connection is cut and
// the client sees a short read.
func (s *Server) serveFlight(w http.ResponseWriter, r *http.Request, t fetch.Target, client *hub.Client) {
	start, end, status, ok := parseRange(r.Header.Get("Range"), t.Size)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", t.Size))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	f := s.join(t, client)

	// Don't commit to a status until the first byte we need exists, so an
	// early failure (upstream gone, gated) can still become a proper error.
	if err := waitFor(r.Context(), f, start+1, t.Size); err != nil {
		if r.Context().Err() == nil {
			writeError(w, err)
		}
		return
	}

	h := w.Header()
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if status == http.StatusPartialContent {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, t.Size))
	}
	w.WriteHeader(status)

	buf := make([]byte, 4<<20)
	for pos := start; pos <= end; {
		if err := waitFor(r.Context(), f, pos+1, t.Size); err != nil {
			panic(http.ErrAbortHandler) // cut the connection: headers are gone
		}
		st := f.state()
		limit := st.written // exclusive
		if !st.done && limit > t.Size-1 {
			limit = t.Size - 1 // hold back the last byte until verified
		}
		if st.done {
			limit = t.Size
		}
		n := min(int64(len(buf)), limit-pos, end+1-pos)
		got, err := readChunk(f, buf[:n], pos, st.done)
		if err != nil {
			s.log.Error("read while streaming", "path", t.Path, "err", err)
			panic(http.ErrAbortHandler)
		}
		if _, err := w.Write(buf[:got]); err != nil {
			return // client went away
		}
		pos += int64(got)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}
}

// waitFor blocks until the flight has at least need bytes that may be sent
// (or has finished), and returns the flight's error if it failed.
func waitFor(ctx context.Context, f *flight, need, size int64) error {
	for {
		st := f.state()
		if st.done {
			return st.err
		}
		if min(st.written, size-1) >= need {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-st.changed:
		}
	}
}

// readChunk reads from the partial file, or from the blob once it has been
// committed. The file is opened for each chunk so no handle stays open
// across the rename (Windows can't rename open files).
func readChunk(f *flight, buf []byte, off int64, done bool) (int, error) {
	paths := []string{f.part, f.blob}
	if done {
		paths = []string{f.blob}
	}
	var lastErr error
	for _, p := range paths {
		file, err := os.Open(p)
		if err != nil {
			lastErr = err
			continue
		}
		n, err := file.ReadAt(buf, off)
		file.Close()
		if n > 0 || err == nil {
			return n, nil
		}
		lastErr = err
	}
	if errors.Is(lastErr, io.EOF) {
		return 0, fmt.Errorf("partial file shorter than reported progress")
	}
	return 0, lastErr
}

// parseRange handles the single ranges clients send ("bytes=a-b", "bytes=a-",
// "bytes=-n"). No header means the whole file with status 200.
func parseRange(h string, size int64) (start, end int64, status int, ok bool) {
	if h == "" {
		return 0, size - 1, http.StatusOK, true
	}
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, size - 1, http.StatusOK, true // ignore what we don't support, like net/http
	}
	a, b, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return 0, 0, 0, false
	}
	if a == "" { // suffix range: the last n bytes
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, 0, false
		}
		return max(size-n, 0), size - 1, http.StatusPartialContent, true
	}
	var err error
	if start, err = strconv.ParseInt(a, 10, 64); err != nil || start < 0 || start >= size {
		return 0, 0, 0, false
	}
	end = size - 1
	if b != "" {
		if end, err = strconv.ParseInt(b, 10, 64); err != nil || end < start {
			return 0, 0, 0, false
		}
		end = min(end, size-1)
	}
	return start, end, http.StatusPartialContent, true
}

func writeUnavailable(w http.ResponseWriter, msg string) {
	w.Header().Set("X-Error-Message", msg)
	http.Error(w, msg, http.StatusGatewayTimeout)
}

// writeError maps an error to the response a Hub client understands.
// Upstream 4xx answers pass through with their codes; anything that means
// "couldn't reach the Hub" is a 504, never a not-found.
func writeError(w http.ResponseWriter, err error) {
	h := w.Header()
	var he *hub.HTTPError
	var denied *deniedError
	switch {
	case errors.As(err, &denied):
		h.Set("X-Error-Code", "Denylisted")
		h.Set("X-Error-Message", denied.Error())
		http.Error(w, denied.Error(), http.StatusUnavailableForLegalReasons)
	case errors.As(err, &he) && he.Status >= 400 && he.Status < 500 && he.Status != http.StatusTooManyRequests && he.Status != http.StatusRequestTimeout:
		if he.Code != "" {
			h.Set("X-Error-Code", he.Code)
		}
		if he.Message != "" {
			h.Set("X-Error-Message", he.Message)
		}
		if he.Commit != "" {
			h.Set("X-Repo-Commit", he.Commit)
		}
		status := he.Status
		if status == http.StatusUnauthorized && he.Code == "" && errors.Is(err, hub.ErrRepoNotFound) {
			// Our own upstream token made the request, so a 401 without a
			// code means "no such repo" to us; say it plainly.
			status = http.StatusNotFound
			h.Set("X-Error-Code", "RepoNotFound")
		}
		http.Error(w, firstNonEmpty(he.Message, http.StatusText(status)), status)
	case errors.Is(err, manifest.ErrNotFound):
		writeUnavailable(w, "not kept locally, and the upstream Hub could not be asked: "+err.Error())
	case isUnavailable(err):
		writeUnavailable(w, "upstream Hub unavailable: "+err.Error())
	default:
		http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
