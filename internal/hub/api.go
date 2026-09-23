package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// maxAPIBody bounds JSON responses we read into memory. Repo info for the
// largest repos is a few MB.
const maxAPIBody = 64 << 20

// RepoInfo is the subset of /api/models/{id}/revision/{rev} weightkeep uses.
// Raw keeps the whole response so the proxy can serve it later.
type RepoInfo struct {
	ID           string     `json:"id"`
	SHA          string     `json:"sha"`
	Private      bool       `json:"private"`
	Gated        GatedState `json:"gated"`
	Disabled     bool       `json:"disabled"`
	LastModified time.Time  `json:"lastModified"`
	Siblings     []struct {
		RFilename string `json:"rfilename"`
	} `json:"siblings"`
	CardData CardData `json:"cardData"`

	Raw json.RawMessage `json:"-"`
}

// CardData holds the model card fields that matter for licensing.
type CardData struct {
	License     StringList `json:"license"`
	LicenseName string     `json:"license_name"`
	LicenseLink string     `json:"license_link"`
	BaseModel   StringList `json:"base_model"`
}

// GatedState is "" for ungated repos, else "auto" or "manual". The Hub sends
// false, true, "auto" or "manual".
type GatedState string

func (g *GatedState) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "false", "null":
		*g = ""
		return nil
	case "true":
		*g = "true"
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("gated: %w", err)
	}
	*g = GatedState(s)
	return nil
}

// IsGated reports whether downloading needs the author's approval.
func (g GatedState) IsGated() bool { return g != "" }

// StringList accepts a JSON string or a list of strings.
type StringList []string

func (l *StringList) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*l = nil
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*l = StringList{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	*l = list
	return nil
}

// RepoInfo resolves rev (a branch, tag or commit) and returns the repo's
// metadata at that revision.
func (c *Client) RepoInfo(ctx context.Context, repo Repo, rev string) (*RepoInfo, error) {
	u := c.url("api/"+repo.Type.APISegment()+"/"+repo.escapedID()+"/revision/"+url.PathEscape(rev), nil)
	resp, err := c.getAPI(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBody))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", redactURL(u), err)
	}
	var info RepoInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode %s: %w", redactURL(u), err)
	}
	if !IsCommit(info.SHA) {
		return nil, fmt.Errorf("%s: response has no commit sha", redactURL(u))
	}
	info.Raw = raw
	return &info, nil
}

// TreeEntry is one entry of a tree listing.
type TreeEntry struct {
	Type string   `json:"type"` // "file" or "directory"
	Path string   `json:"path"`
	Size int64    `json:"size"`
	OID  string   `json:"oid"` // git blob SHA-1 (of the LFS pointer for LFS files)
	LFS  *LFSInfo `json:"lfs,omitempty"`
}

// LFSInfo is present for files stored in LFS or Xet.
type LFSInfo struct {
	OID         string `json:"oid"` // SHA-256 of the content
	Size        int64  `json:"size"`
	PointerSize int64  `json:"pointerSize"`
}

// IsFile reports whether the entry is a file.
func (e TreeEntry) IsFile() bool { return e.Type == "file" }

// ETag is the id the Hub serves as the file's ETag: SHA-256 for LFS files,
// the git blob SHA-1 otherwise.
func (e TreeEntry) ETag() string {
	if e.LFS != nil {
		return e.LFS.OID
	}
	return e.OID
}

// Tree lists every entry at commit, recursively, following pagination.
func (c *Client) Tree(ctx context.Context, repo Repo, commit string) ([]TreeEntry, error) {
	q := url.Values{"recursive": {"true"}, "expand": {"false"}}
	next := c.url("api/"+repo.Type.APISegment()+"/"+repo.escapedID()+"/tree/"+url.PathEscape(commit), q)
	var all []TreeEntry
	for pages := 0; next != ""; pages++ {
		if pages >= 10_000 {
			return nil, fmt.Errorf("tree %s@%s: too many pages", repo, commit)
		}
		resp, err := c.getAPI(ctx, next)
		if err != nil {
			return nil, err
		}
		var page []TreeEntry
		dec := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBody))
		err = dec.Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode tree %s@%s: %w", repo, commit, err)
		}
		all = append(all, page...)
		if next, err = c.nextLink(link); err != nil {
			return nil, err
		}
	}
	for _, e := range all {
		if e.IsFile() {
			if err := ValidatePath(e.Path); err != nil {
				return nil, fmt.Errorf("tree %s@%s: %w", repo, commit, err)
			}
		}
	}
	return all, nil
}

// nextLink finds rel="next" in a Link header. The next page must be on the
// Hub's own host.
func (c *Client) nextLink(h string) (string, error) {
	for _, part := range strings.Split(h, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok || !strings.Contains(strings.ReplaceAll(params, " ", ""), `rel="next"`) {
			continue
		}
		raw := strings.Trim(strings.TrimSpace(target), "<>")
		u, err := c.base.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("bad Link header %q: %w", h, err)
		}
		if u.Host != c.base.Host {
			return "", fmt.Errorf("pagination link points to another host: %s", u.Host)
		}
		return u.String(), nil
	}
	return "", nil
}
