// Package proxy serves kept revisions through the subset of the Hugging Face
// Hub HTTP API that clients use, so HF_ENDPOINT can point at weightkeep.
//
// File bytes always come from the store. When online, revisions and files
// that aren't kept yet are fetched from the upstream Hub on first request.
// The wire rules this package follows are in ADR 0004 and the hf-protocol
// skill; the important ones: X-Repo-Commit on every resolve response, ETags
// that match the tree listing, no Xet signals, and upstream failures
// reported as 502/504 rather than "not found".
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/keep"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

// Options configures a Server.
type Options struct {
	// Offline never contacts the upstream Hub.
	Offline bool
	// RefTTL is how long a branch or tag resolution is trusted before the
	// upstream is asked again. Default 60s.
	RefTTL time.Duration
	Log    *slog.Logger
}

// Server is an http.Handler.
type Server struct {
	k       *keep.Keeper
	offline bool
	refTTL  time.Duration
	log     *slog.Logger

	resolving singleflight.Group
	mu        sync.Mutex
	refs      map[refKey]refEntry
	flights   *flights
}

type refKey struct {
	repo hub.Repo
	rev  string
}

type refEntry struct {
	commit string
	at     time.Time
}

// New returns a Server over the keeper's store and upstream.
func New(k *keep.Keeper, opts Options) *Server {
	if opts.RefTTL == 0 {
		opts.RefTTL = 60 * time.Second
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Server{
		k:       k,
		offline: opts.Offline || k.Hub == nil,
		refTTL:  opts.RefTTL,
		log:     opts.Log,
		refs:    map[refKey]refEntry{},
		flights: newFlights(),
	}
}

// ServeHTTP routes a request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"bytes", rec.bytes, "duration", time.Since(start).Round(time.Millisecond))
	}()

	rt, err := parseRoute(r.URL.EscapedPath())
	if err != nil {
		http.Error(rec, err.Error(), http.StatusBadRequest)
		return
	}
	switch rt.kind {
	case routeResolve:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(rec, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleResolve(rec, r, rt)
	default:
		s.handleAPI(rec, r, rt)
	}
}

// revision returns the manifest for repo@rev, learning the revision from
// the upstream (small files only) if it isn't kept yet.
func (s *Server) revision(ctx context.Context, repo hub.Repo, rev string) (*manifest.Manifest, error) {
	mrepo := manifest.Repo{Type: string(repo.Type), ID: repo.ID}
	if hub.IsCommit(rev) {
		if m, err := manifest.Load(ctx, s.k.Store, mrepo, rev); err == nil {
			return m, nil
		}
	} else if commit, ok := s.cachedRef(repo, rev); ok {
		if m, err := manifest.Load(ctx, s.k.Store, mrepo, commit); err == nil {
			return m, nil
		}
	}

	if s.offline {
		return s.localRevision(ctx, mrepo, rev)
	}

	key := string(repo.Type) + "/" + repo.ID + "@" + rev
	v, err, _ := s.resolving.Do(key, func() (any, error) {
		// Detached from the request: if the client gives up, the next one
		// still benefits from the work.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		res, err := s.k.Pull(ctx, keep.PullRequest{Repo: repo, Revision: rev, NoLFS: true})
		if err != nil {
			return nil, err
		}
		return res.Manifest, nil
	})
	if err != nil {
		if hub.Retryable(err) {
			// The Hub is down: answer from what we know.
			if m, lerr := s.localRevision(ctx, mrepo, rev); lerr == nil {
				s.log.Warn("upstream unavailable, serving kept revision", "repo", repo.String(), "rev", rev, "err", err)
				return m, nil
			}
		}
		return nil, err
	}
	m := v.(*manifest.Manifest)
	if !hub.IsCommit(rev) {
		s.setCachedRef(repo, rev, m.Commit)
	}
	return m, nil
}

func (s *Server) localRevision(ctx context.Context, repo manifest.Repo, rev string) (*manifest.Manifest, error) {
	commit, _, err := manifest.ResolveRef(ctx, s.k.Store, repo, rev)
	if err != nil {
		return nil, err
	}
	return manifest.Load(ctx, s.k.Store, repo, commit)
}

func (s *Server) cachedRef(repo hub.Repo, rev string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.refs[refKey{repo, rev}]
	if !ok || time.Since(e.at) > s.refTTL {
		return "", false
	}
	return e.commit, true
}

func (s *Server) setCachedRef(repo hub.Repo, rev, commit string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[refKey{repo, rev}] = refEntry{commit, time.Now()}
}

// isOffline reports whether an error means "we can't reach the Hub".
func isUnavailable(err error) bool {
	return hub.Retryable(err) || errors.Is(err, context.DeadlineExceeded)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
