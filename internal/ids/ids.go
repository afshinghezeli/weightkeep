// Package ids validates the identifiers weightkeep turns into file names:
// repo ids, commit ids, content hashes and repo file paths. Anything that
// passes here is safe to join onto a directory.
package ids

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// The Hub allows letters, digits, "-", "_" and "." in names, up to 96
	// characters.
	segmentRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)
	commitRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256RE  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidateRepoID accepts "namespace/name" and legacy single-segment ids.
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

// IsCommit reports whether s is a full 40-hex lowercase commit id.
func IsCommit(s string) bool { return commitRE.MatchString(s) }

// IsSHA256 reports whether s is 64 lowercase hex characters.
func IsSHA256(s string) bool { return sha256RE.MatchString(s) }

// ValidatePath rejects repo file paths that are empty, absolute, contain
// backslashes or NUL, or have empty, "." or ".." segments. Paths use "/"
// on every OS.
func ValidatePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return fmt.Errorf("invalid repo path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid repo path %q", p)
		}
	}
	return nil
}
