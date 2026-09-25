// Package manifest records what a kept revision contains: every file's path,
// size and hashes, where it was fetched from, and the licence metadata seen
// at the time.
//
// A manifest is written as canonical JSON to
// manifests/<type>s/<repo id>/<commit>.json under the store root and indexed
// in SQLite. The file is the record; the tables are an index that can be
// rebuilt from the files.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/ids"
)

// FormatVersion is the manifest format this code writes. Readers reject
// newer versions.
const FormatVersion = 1

// Manifest describes one kept revision.
type Manifest struct {
	Version   int       `json:"version"`
	Repo      Repo      `json:"repo"`
	Commit    string    `json:"commit"`
	FetchedAt time.Time `json:"fetched_at"`
	Upstream  string    `json:"upstream"`
	License   License   `json:"license"`
	Files     []File    `json:"files"`
}

// Repo identifies the repository. Type is "model", "dataset" or "space".
type Repo struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Name is the part of the id after the namespace.
func (r Repo) Name() string {
	if _, name, ok := strings.Cut(r.ID, "/"); ok {
		return name
	}
	return r.ID
}

func (r Repo) String() string {
	if r.Type == "model" {
		return r.ID
	}
	return r.Type + "s/" + r.ID
}

// License is the licence metadata the Hub reported for this revision.
// Tier decisions are made from it later (internal/policy).
type License struct {
	IDs   []string `json:"ids,omitempty"`  // cardData.license
	Name  string   `json:"name,omitempty"` // cardData.license_name
	Link  string   `json:"link,omitempty"` // cardData.license_link
	Gated string   `json:"gated,omitempty"`
	// BaseModels is cardData.base_model, for licence inheritance.
	BaseModels []string `json:"base_models,omitempty"`
}

// File is one file of the revision.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// GitSHA1 is the blob id in the repo's git tree, as the Hub's tree
	// listing reports it: the content's id for regular files, the LFS
	// pointer's id for LFS files. Keeping the tree's value lets an offline
	// tree listing match the Hub's exactly.
	GitSHA1 string `json:"git_sha1"`
	// LFS is true for files the Hub stores in LFS/Xet. It decides which
	// hash the Hub (and so our proxy) uses as the ETag.
	LFS bool `json:"lfs"`
}

// ETag is the id the Hub serves for this file: SHA-256 for LFS files, the
// git blob SHA-1 of the content otherwise.
func (f File) ETag() string {
	if f.LFS {
		return f.SHA256
	}
	return f.GitSHA1
}

// Size returns the total size of all files.
func (m *Manifest) Size() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

// File returns the file at path.
func (m *Manifest) File(path string) (File, bool) {
	i := sort.Search(len(m.Files), func(i int) bool { return m.Files[i].Path >= path })
	if i < len(m.Files) && m.Files[i].Path == path {
		return m.Files[i], true
	}
	return File{}, false
}

// Validate checks that the manifest is well formed and safe to use for
// building paths.
func (m *Manifest) Validate() error {
	if m.Version != FormatVersion {
		return fmt.Errorf("manifest format version %d is not supported (this weightkeep reads version %d)", m.Version, FormatVersion)
	}
	switch m.Repo.Type {
	case "model", "dataset", "space":
	default:
		return fmt.Errorf("manifest: unknown repo type %q", m.Repo.Type)
	}
	if err := ids.ValidateRepoID(m.Repo.ID); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if !ids.IsCommit(m.Commit) {
		return fmt.Errorf("manifest: commit %q is not a 40-hex commit id", m.Commit)
	}
	seen := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		if err := ids.ValidatePath(f.Path); err != nil {
			return fmt.Errorf("manifest: %w", err)
		}
		if seen[f.Path] {
			return fmt.Errorf("manifest: %q listed twice", f.Path)
		}
		seen[f.Path] = true
		if !ids.IsSHA256(f.SHA256) {
			return fmt.Errorf("manifest: %s: bad sha256 %q", f.Path, f.SHA256)
		}
		if !ids.IsCommit(f.GitSHA1) { // same shape: 40 hex
			return fmt.Errorf("manifest: %s: bad git sha1 %q", f.Path, f.GitSHA1)
		}
		if f.Size < 0 {
			return fmt.Errorf("manifest: %s: negative size", f.Path)
		}
	}
	return nil
}

// Canonical returns the manifest's canonical encoding: files sorted by path,
// time in UTC with second precision, no insignificant whitespace, no HTML
// escaping, one trailing newline. Equal manifests encode to equal bytes.
func (m *Manifest) Canonical() ([]byte, error) {
	c := *m
	c.FetchedAt = m.FetchedAt.UTC().Truncate(time.Second)
	c.Files = append([]File(nil), m.Files...)
	sort.Slice(c.Files, func(i, j int) bool { return c.Files[i].Path < c.Files[j].Path })
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(&c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Parse decodes and validates a manifest. Unknown fields are an error, so a
// newer format can't be half-read.
func Parse(data []byte) (*Manifest, error) {
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if probe.Version > FormatVersion {
		return nil, fmt.Errorf("manifest format version %d is newer than this weightkeep understands; upgrade weightkeep", probe.Version)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Digest is the SHA-256 of the canonical encoding.
func Digest(canonical []byte) string {
	s := sha256.Sum256(canonical)
	return hex.EncodeToString(s[:])
}

// ErrNotFound means no manifest is kept for that revision.
var ErrNotFound = errors.New("revision not kept")

// Diff lists how got's files differ from want's: missing or extra paths, and
// different sizes or hashes. A SHA-256 is compared only when both sides have
// one (a regular file's is filled in after download); its git id, which
// identifies the content just as well, always is.
func Diff(want, got *Manifest) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range want.Files {
		seen[w.Path] = true
		g, ok := got.File(w.Path)
		switch {
		case !ok:
			out = append(out, w.Path+": missing")
		case g.Size != w.Size:
			out = append(out, fmt.Sprintf("%s: %d bytes, expected %d", w.Path, g.Size, w.Size))
		case g.GitSHA1 != w.GitSHA1:
			out = append(out, fmt.Sprintf("%s: git id %s, expected %s", w.Path, g.GitSHA1, w.GitSHA1))
		case g.SHA256 != "" && w.SHA256 != "" && g.SHA256 != w.SHA256:
			out = append(out, fmt.Sprintf("%s: sha256 %s, expected %s", w.Path, g.SHA256, w.SHA256))
		}
	}
	for _, g := range got.Files {
		if !seen[g.Path] {
			out = append(out, g.Path+": not expected")
		}
	}
	return out
}
