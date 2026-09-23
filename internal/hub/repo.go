package hub

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/afshinghezeli/weightkeep/internal/ids"
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

// ValidateRepoID checks an id without a type prefix.
func ValidateRepoID(id string) error { return ids.ValidateRepoID(id) }

// IsCommit reports whether rev is a full 40-hex commit id.
func IsCommit(rev string) bool { return ids.IsCommit(rev) }

// ValidatePath rejects repo file paths that could escape a directory.
func ValidatePath(p string) error { return ids.ValidatePath(p) }

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
