// Package hub talks to the upstream Hugging Face Hub (or anything that
// speaks its API): repository metadata, file listings and file bytes.
//
// The token is attached by the transport, and only to requests for the
// configured Hub host. Redirects to CDNs therefore never carry it, whatever
// the redirect chain looks like.
package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/config"
)

// Client is a Hub API client. It is safe for concurrent use.
type Client struct {
	base     *url.URL
	api      *http.Client // follows same-host redirects only
	download *http.Client // follows redirects to CDNs
	retries  int
	backoff  time.Duration
}

// Options configures a Client. The zero value is usable.
type Options struct {
	Token     config.Token
	UserAgent string
	// Transport overrides the base transport (tests).
	Transport http.RoundTripper
	// APITimeout bounds each metadata request. Default 60s.
	APITimeout time.Duration
}

// New returns a client for the Hub at base (for example https://huggingface.co).
func New(base string, opts Options) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("hub base URL %q: must be an http or https URL", base)
	}
	rt := opts.Transport
	if rt == nil {
		rt = defaultTransport()
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "weightkeep"
	}
	t := &authTransport{base: rt, host: u.Host, token: opts.Token, userAgent: ua}

	apiTimeout := opts.APITimeout
	if apiTimeout == 0 {
		apiTimeout = 60 * time.Second
	}
	c := &Client{
		base:    u,
		retries: 3,
		backoff: 500 * time.Millisecond,
		api: &http.Client{
			Transport: t,
			Timeout:   apiTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				// The Hub canonicalises legacy and mis-cased ids with a
				// relative 307. Anything off-host is not an API answer.
				if req.URL.Host != u.Host {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		download: &http.Client{
			Transport: t,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
	return c, nil
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		// Never let the transport decompress: we hash exact bytes.
		DisableCompression: true,
	}
}

// Base returns the Hub URL this client talks to.
func (c *Client) Base() string { return c.base.String() }

// authTransport adds the User-Agent everywhere and the token only for the
// Hub's own host.
type authTransport struct {
	base      http.RoundTripper
	host      string
	token     config.Token
	userAgent string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Del("Authorization")
	if req.URL.Host == t.host && !t.token.IsZero() {
		req.Header.Set("Authorization", "Bearer "+t.token.Value())
	}
	return t.base.RoundTrip(req)
}

// url builds base + "/" + escapedPath. url.Parse (inside NewRequest) keeps
// escapes like %2F in revisions intact.
func (c *Client) url(escapedPath string, query url.Values) string {
	s := c.base.String() + "/" + escapedPath
	if len(query) > 0 {
		s += "?" + query.Encode()
	}
	return s
}

// getAPI runs a GET with retries on transient failures and returns
// the successful response. The caller closes the body.
func (c *Client) getAPI(ctx context.Context, rawURL string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.backoff << (attempt - 1)):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.api.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("GET %s: %w", redactURL(rawURL), err)
			if Retryable(lastErr) {
				continue
			}
			return nil, lastErr
		}
		if resp.StatusCode < 300 {
			return resp, nil
		}
		herr := classify(resp)
		resp.Body.Close()
		if resp.StatusCode < 400 {
			herr.Message = "unexpected redirect to " + redactURL(resp.Header.Get("Location"))
			return nil, herr
		}
		lastErr = herr
		if !Retryable(herr) {
			return nil, herr
		}
	}
	return nil, lastErr
}

// FileMeta is what a HEAD on a resolve URL says about a file.
type FileMeta struct {
	Commit string // X-Repo-Commit
	ETag   string // unquoted: git SHA-1 for regular files, SHA-256 for LFS
	Size   int64  // -1 if the Hub didn't say
}

// FileMeta asks the Hub about one file without downloading it. rev may be a
// branch, tag or commit.
func (c *Client) FileMeta(ctx context.Context, repo Repo, rev, path string) (FileMeta, error) {
	if err := ValidatePath(path); err != nil {
		return FileMeta{}, err
	}
	u := c.resolveURL(repo, rev, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return FileMeta{}, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.api.Do(req)
	if err != nil {
		return FileMeta{}, fmt.Errorf("HEAD %s: %w", redactURL(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return FileMeta{}, classify(resp)
	}
	m := FileMeta{Commit: resp.Header.Get("X-Repo-Commit"), Size: -1}
	etag := resp.Header.Get("X-Linked-Etag")
	if etag == "" {
		etag = resp.Header.Get("ETag")
	}
	m.ETag = normalizeETag(etag)
	if s := resp.Header.Get("X-Linked-Size"); s != "" {
		m.Size, _ = strconv.ParseInt(s, 10, 64)
	} else if resp.StatusCode < 300 && resp.ContentLength >= 0 {
		m.Size = resp.ContentLength
	}
	if m.Commit == "" {
		return m, fmt.Errorf("HEAD %s: no X-Repo-Commit header; %s does not look like a Hugging Face Hub", redactURL(u), c.base.Host)
	}
	return m, nil
}

// Body is a file download in progress.
type Body struct {
	io.ReadCloser
	// Offset is where the body starts. It is 0 when the server ignored the
	// Range request, even if a later offset was asked for.
	Offset int64
	// Total is the full file size if the server said, else -1.
	Total int64
}

// Download fetches a file starting at offset. Redirects to CDNs are followed;
// the token is only ever sent to the Hub itself.
func (c *Client) Download(ctx context.Context, repo Repo, rev, path string, offset int64) (*Body, error) {
	if err := ValidatePath(path); err != nil {
		return nil, err
	}
	u := c.resolveURL(repo, rev, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := c.download.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", redactURL(u), err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return &Body{ReadCloser: resp.Body, Offset: 0, Total: resp.ContentLength}, nil
	case http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset {
			resp.Body.Close()
			return nil, fmt.Errorf("GET %s: asked for bytes from %d, got Content-Range %q", redactURL(u), offset, resp.Header.Get("Content-Range"))
		}
		return &Body{ReadCloser: resp.Body, Offset: start, Total: total}, nil
	}
	defer resp.Body.Close()
	return nil, classify(resp)
}

func (c *Client) resolveURL(repo Repo, rev, path string) string {
	return c.url(repo.Type.URLPrefix()+repo.escapedID()+"/resolve/"+url.PathEscape(rev)+"/"+escapePath(path), nil)
}

// parseContentRange parses "bytes 100-199/1000". total is -1 for "*".
func parseContentRange(h string) (start, total int64, ok bool) {
	rest, found := strings.CutPrefix(h, "bytes ")
	if !found {
		return 0, 0, false
	}
	rng, tot, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	first, _, found := strings.Cut(rng, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	total = -1
	if tot != "*" {
		if total, err = strconv.ParseInt(tot, 10, 64); err != nil {
			return 0, 0, false
		}
	}
	return start, total, true
}

// normalizeETag strips W/ and quotes, like huggingface_hub's _normalize_etag.
func normalizeETag(e string) string {
	e = strings.TrimPrefix(e, "W/")
	return strings.Trim(e, `"`)
}

// redactURL drops the query string, which on CDN URLs carries signatures.
func redactURL(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i] + "?…"
	}
	return raw
}
