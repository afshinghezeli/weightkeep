package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Sentinel errors. Use errors.Is; the concrete error is *HTTPError when the
// Hub answered, or a wrapped transport error.
var (
	ErrRepoNotFound     = errors.New("repository not found")
	ErrRevisionNotFound = errors.New("revision not found")
	ErrEntryNotFound    = errors.New("file not found in repository")
	ErrGated            = errors.New("repository is gated")
	ErrDisabled         = errors.New("repository has been disabled by the Hub")
	ErrUnauthorized     = errors.New("token rejected by the Hub")
	ErrUnavailable      = errors.New("hub unavailable")
)

// HTTPError is a non-success answer from the Hub.
type HTTPError struct {
	Method  string
	URL     string
	Status  int
	Code    string // X-Error-Code
	Message string // X-Error-Message, or the body's "error"
	Commit  string // X-Repo-Commit, sent with EntryNotFound
	kind    error
}

func (e *HTTPError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.URL, e.Status, msg)
}

func (e *HTTPError) Unwrap() error { return e.kind }

// classify turns a non-2xx/3xx response into an *HTTPError. It follows the
// same rules as huggingface_hub's hf_raise_for_status, so we reach the same
// conclusion a Python client would.
func classify(resp *http.Response) *HTTPError {
	e := &HTTPError{
		Method:  resp.Request.Method,
		URL:     redactURL(resp.Request.URL.String()),
		Status:  resp.StatusCode,
		Code:    resp.Header.Get("X-Error-Code"),
		Message: resp.Header.Get("X-Error-Message"),
		Commit:  resp.Header.Get("X-Repo-Commit"),
	}
	if e.Message == "" && resp.Request.Method != http.MethodHead {
		e.Message = bodyMessage(resp.Body)
	}

	switch {
	case e.Code == "RevisionNotFound":
		e.kind = ErrRevisionNotFound
	case e.Code == "EntryNotFound":
		e.kind = ErrEntryNotFound
	case e.Code == "GatedRepo":
		e.kind = ErrGated
	case e.Message == "Access to this resource is disabled.":
		e.kind = ErrDisabled
	case e.Code == "RepoNotFound":
		e.kind = ErrRepoNotFound
	case e.Status == http.StatusUnauthorized && e.Message == "Invalid credentials in Authorization header":
		e.kind = ErrUnauthorized
	case e.Status == http.StatusUnauthorized:
		// The Hub answers 401 for repos that don't exist (or are private)
		// when the request has no token.
		e.kind = ErrRepoNotFound
	case e.Status == http.StatusNotFound:
		e.kind = ErrRepoNotFound
	case e.Status == http.StatusTooManyRequests || e.Status == http.StatusRequestTimeout || e.Status >= 500:
		e.kind = ErrUnavailable
	}
	return e
}

func bodyMessage(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 4096))
	var v struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &v) == nil && len(v.Error) > 0 {
		var s string
		if json.Unmarshal(v.Error, &s) == nil {
			return s
		}
		var list []string
		if json.Unmarshal(v.Error, &list) == nil {
			return strings.Join(list, "; ")
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 || strings.HasPrefix(s, "<") {
		return ""
	}
	return s
}

// Retryable reports whether err is worth retrying later: the Hub was down
// or overloaded, or the network failed. Answers like "not found" are final,
// and so is the caller giving up.
func Retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	var he *HTTPError
	return !errors.As(err, &he) // transport errors: reset, timeout, DNS
}
