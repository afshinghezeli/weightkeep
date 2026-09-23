package hub

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// RepoType is the kind of Hub repository.
type RepoType string

const (
	Model   RepoType = "model"
	Dataset RepoType = "dataset"
	Space   RepoType = "space"
)

// ParseRepoType accepts the singular and plural forms used in URLs.
func ParseRepoType(s string) (RepoType, error) {
	switch s {
	case "", "model", "models":
		return Model, nil
	case "dataset", "datasets":
		return Dataset, nil
	case "space", "spaces":
		return Space, nil
	}
	return "", fmt.Errorf("unknown repo type %q", s)
}

// URLPrefix is the prefix before the repo id in resolve URLs: "" for
// models, "datasets/" and "spaces/" otherwise.
func (t RepoType) URLPrefix() string {
	if t == Model {
		return ""
	}
	return string(t) + "s/"
}

// APISegment is the collection name in API URLs: "models", "datasets", "spaces".
func (t RepoType) APISegment() string { return string(t) + "s" }

// Repo identifies a repository on the Hub.
type Repo struct {
	Type RepoType
	ID   string // "namespace/name", or a legacy single segment like "gpt2"
}

func (r Repo) String() string {
	if r.Type == Model || r.Type == "" {
		return r.ID
	}
	return r.Type.URLPrefix() + r.ID
}

// Namespace returns the part before the slash, or "" for legacy ids.
func (r Repo) Namespace() string {
	ns, _, ok := strings.Cut(r.ID, "/")
	if !ok {
		return ""
	}
	return ns
}

// Name returns the part after the slash, or the whole id for legacy ids.
func (r Repo) Name() string {
	if _, name, ok := strings.Cut(r.ID, "/"); ok {
		return name
	}
	return r.ID
}

// escapedID is the id with each segment path-escaped.
func (r Repo) escapedID() string {
	parts := strings.Split(r.ID, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// The Hub allows letters, digits, "-", "_" and "." in names, up to 96
// characters, and doesn't allow names made only of dots.
var segmentRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

// ParseRepo parses "org/name" (or a legacy "name") as a model repo.
// "datasets/org/name" and "spaces/org/name" set the type.
func ParseRepo(s string) (Repo, error) {
	r := Repo{Type: Model, ID: s}
	for _, t := range []RepoType{Dataset, Space} {
		if rest, ok := strings.CutPrefix(s, t.URLPrefix()); ok {
			r = Repo{Type: t, ID: rest}
		}
	}
	if err := ValidateRepoID(r.ID); err != nil {
		return Repo{}, err
	}
	return r, nil
}

// ValidateRepoID checks an id without a type prefix. It rejects anything
// that could escape a path when the id is used to build file names.
func ValidateRepoID(id string) error {
	parts := strings.Split(id, "/")
	if len(parts) > 2 {
		return fmt.Errorf("repo id %q: expected namespace/name", id)
	}
	for _, p := range parts {
		if !segmentRE.MatchString(p) || strings.Contains(p, "..") {
			return fmt.Errorf("repo id %q: %q is not a valid name", id, p)
		}
	}
	return nil
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// IsCommit reports whether rev is a full 40-hex commit id.
func IsCommit(rev string) bool { return commitRE.MatchString(rev) }

// ValidatePath rejects repo file paths that are absolute, empty, or climb
// out of the repo with "..". Paths use "/" on every OS.
func ValidatePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) {
		return fmt.Errorf("invalid repo path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid repo path %q", p)
		}
	}
	return nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
