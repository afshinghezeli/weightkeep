// Package registry reads and builds the community registry: a static TUF
// repository (go-tuf v2) whose targets are revision records and a denylist.
//
// TUF makes every mirror of the registry untrusted: metadata is signed by
// maintainer keys with thresholds, and the client detects rollback and
// freeze attacks through versions and expiry. See ADR 0006.
//
// Layout of a published registry:
//
//	metadata/1.root.json, root.json, targets.json, snapshot.json, timestamp.json
//	targets/models/<org>/<name>/<commit>.json    a Record
//	targets/denylist.json                        the Denylist
package registry

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/ids"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

// FormatVersion of Record and Denylist.
const FormatVersion = 1

// DenylistTarget is the target path of the denylist.
const DenylistTarget = "denylist.json"

// Record is what the registry says about one revision.
type Record struct {
	Version  int                `json:"version"`
	Manifest *manifest.Manifest `json:"manifest"`
	// Magnet finds the revision's torrent (made by `weightkeep seed`).
	Magnet string `json:"magnet,omitempty"`
	// Added is when the record entered the registry.
	Added time.Time `json:"added"`
}

// RecordTarget is the TUF target path for repo@commit.
func RecordTarget(repo manifest.Repo, commit string) string {
	return repo.Type + "s/" + repo.ID + "/" + commit + ".json"
}

// Validate checks a record before it is published or trusted.
func (r *Record) Validate() error {
	if r.Version != FormatVersion {
		return fmt.Errorf("record format %d is not supported", r.Version)
	}
	if r.Manifest == nil {
		return fmt.Errorf("record has no manifest")
	}
	if err := r.Manifest.Validate(); err != nil {
		return err
	}
	if r.Magnet != "" && !strings.HasPrefix(r.Magnet, "magnet:?") {
		return fmt.Errorf("magnet %q is not a magnet link", r.Magnet)
	}
	return nil
}

// ParseRecord decodes and validates a record.
func ParseRecord(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse record: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return &r, nil
}

// Denylist lists revisions and contents the registry won't help share:
// models removed for legal reasons, authors' opt-out requests.
type Denylist struct {
	Version int         `json:"version"`
	Entries []DenyEntry `json:"entries"`
}

// DenyEntry matches a whole repo, one commit of it, or a file's content.
type DenyEntry struct {
	Repo   string    `json:"repo,omitempty"`   // "org/name" (models) or "datasets/org/name"
	Commit string    `json:"commit,omitempty"` // with Repo: only this revision
	SHA256 string    `json:"sha256,omitempty"` // any file with this content
	Reason string    `json:"reason"`
	Added  time.Time `json:"added"`
}

// Validate checks every entry names something and is well formed.
func (d *Denylist) Validate() error {
	if d.Version != FormatVersion {
		return fmt.Errorf("denylist format %d is not supported", d.Version)
	}
	for i, e := range d.Entries {
		switch {
		case e.Reason == "":
			return fmt.Errorf("denylist entry %d has no reason", i)
		case e.Repo == "" && e.SHA256 == "":
			return fmt.Errorf("denylist entry %d names neither a repo nor a sha256", i)
		case e.SHA256 != "" && !ids.IsSHA256(e.SHA256):
			return fmt.Errorf("denylist entry %d: bad sha256", i)
		case e.Commit != "" && (e.Repo == "" || !ids.IsCommit(e.Commit)):
			return fmt.Errorf("denylist entry %d: a commit needs a repo and 40 hex characters", i)
		}
	}
	return nil
}

// Match returns the first entry that covers the revision, if any.
func (d *Denylist) Match(m *manifest.Manifest) (DenyEntry, bool) {
	if d == nil {
		return DenyEntry{}, false
	}
	repo := m.Repo.String()
	content := map[string]bool{}
	for _, f := range m.Files {
		content[f.SHA256] = true
	}
	for _, e := range d.Entries {
		if e.Repo != "" && strings.EqualFold(e.Repo, repo) && (e.Commit == "" || e.Commit == m.Commit) {
			return e, true
		}
		if e.SHA256 != "" && content[e.SHA256] {
			return e, true
		}
	}
	return DenyEntry{}, false
}
