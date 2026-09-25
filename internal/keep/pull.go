// Package keep implements the operations on kept revisions that commands
// run: pull, verify, garbage collection. It combines the store, manifests,
// the Hub client and the fetcher.
package keep

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/fetch"
	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// Keeper runs operations against one store.
type Keeper struct {
	Store   *store.Store
	Hub     *hub.Client
	Fetcher *fetch.Fetcher
	// Mirrors are tried in order when Hub can't be reached or no longer has
	// a repo or revision (see CanFallBack).
	Mirrors []*hub.Client
	// Deny is the registry's denylist, nil if no registry is configured.
	// Denylisted revisions are tier C: never seeded or fetched from the
	// swarm, and not served to other machines.
	Deny Denylist
	// Registry is the community registry, nil if none is configured.
	// Pull checks what upstream serves against its records.
	Registry Registry
	Now      func() time.Time
}

// Registry is what keep asks the community registry. Implementations
// return ok=false when the registry has no record, not an error.
type Registry interface {
	// Record is the registry's manifest and magnet link for repo@commit.
	Record(repo manifest.Repo, commit string) (m *manifest.Manifest, magnet string, ok bool, err error)
	// Latest is the most recently added record for repo.
	Latest(repo manifest.Repo) (m *manifest.Manifest, magnet string, ok bool, err error)
}

// ErrRegistryMismatch means upstream serves different files for a commit
// than the community registry recorded.
var ErrRegistryMismatch = errors.New("upstream contradicts the registry")

// Denylist says whether a revision must not be shared, and why.
type Denylist interface {
	Denied(m *manifest.Manifest) (reason string, denied bool)
}

// Denied checks m against the denylist, if there is one.
func (k *Keeper) Denied(m *manifest.Manifest) (string, bool) {
	if k.Deny == nil {
		return "", false
	}
	return k.Deny.Denied(m)
}

// ClientFor returns the client for the upstream a manifest came from, so
// its files are fetched from the same place. It falls back to Hub.
func (k *Keeper) ClientFor(upstream string) *hub.Client {
	for _, m := range k.Mirrors {
		if m.Base() == upstream {
			return m
		}
	}
	return k.Hub
}

// fetcherFor is the fetcher bound to client.
func (k *Keeper) fetcherFor(client *hub.Client) *fetch.Fetcher {
	if client == k.Hub {
		return k.Fetcher
	}
	f := *k.Fetcher
	f.Hub = client
	return &f
}

// CanFallBack reports whether a failed lookup on the primary upstream
// should be retried elsewhere (mirrors, the swarm): the upstream is down,
// or the repo, revision or file is gone. A gated repo or a rejected token
// is never retried elsewhere: that would get around the author's access
// agreement.
func CanFallBack(err error) bool {
	switch {
	case errors.Is(err, hub.ErrGated), errors.Is(err, hub.ErrUnauthorized):
		return false
	case hub.Retryable(err),
		errors.Is(err, hub.ErrRepoNotFound),
		errors.Is(err, hub.ErrRevisionNotFound),
		errors.Is(err, hub.ErrDisabled):
		return true
	}
	return false
}

// repoInfo asks the primary upstream, then the mirrors, and returns the
// answer with the client that gave it.
func (k *Keeper) repoInfo(ctx context.Context, repo hub.Repo, rev string) (*hub.RepoInfo, *hub.Client, error) {
	info, err := k.Hub.RepoInfo(ctx, repo, rev)
	if err == nil || !CanFallBack(err) {
		return info, k.Hub, err
	}
	primaryErr := err
	for _, m := range k.Mirrors {
		if info, err := m.RepoInfo(ctx, repo, rev); err == nil {
			return info, m, nil
		}
	}
	return nil, nil, primaryErr
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// PullRequest describes what to pull.
type PullRequest struct {
	Repo     hub.Repo
	Revision string // branch, tag or commit; "" means main
	// Include and Exclude are glob patterns matched against file paths,
	// with huggingface_hub semantics ("*" also matches "/"). They choose
	// which LFS files to download. Small (non-LFS) files are always kept:
	// they include the config, tokenizer and licence.
	Include []string
	Exclude []string
	// NoLFS downloads only the small files and records the manifest. The
	// proxy uses it to learn a revision before fetching large files on
	// demand.
	NoLFS bool
}

// PullResult reports what happened.
type PullResult struct {
	Manifest   *manifest.Manifest
	Downloaded []string // files fetched in this run
	Present    []string // selected files that were already kept
	Skipped    []string // LFS files left out by the filters
	// Offline is true when nothing was asked of the Hub (pinned commit
	// already kept).
	Offline bool
	// Warnings are things the user should look at, such as a repo whose
	// history no longer contains a commit the registry lists.
	Warnings []string
}

// FileError collects per-file failures.
type FileError struct {
	Failures []fetch.Result
}

func (e *FileError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d file(s) failed:", len(e.Failures))
	for _, f := range e.Failures {
		fmt.Fprintf(&b, "\n  %s: %v", f.Target.Path, f.Err)
	}
	return b.String()
}

func (e *FileError) Unwrap() []error {
	errs := make([]error, len(e.Failures))
	for i, f := range e.Failures {
		errs[i] = f.Err
	}
	return errs
}

// Pull resolves the revision, downloads the selected files and records the
// manifest and the ref.
func (k *Keeper) Pull(ctx context.Context, req PullRequest) (*PullResult, error) {
	include, err := compileGlobs(req.Include)
	if err != nil {
		return nil, err
	}
	exclude, err := compileGlobs(req.Exclude)
	if err != nil {
		return nil, err
	}
	rev := req.Revision
	if rev == "" {
		rev = "main"
	}
	mrepo := manifest.Repo{Type: string(req.Repo.Type), ID: req.Repo.ID}

	res := &PullResult{}
	var m *manifest.Manifest
	var info *hub.RepoInfo

	if hub.IsCommit(rev) {
		// A pinned commit we already keep needs no network at all.
		if m, err = manifest.Load(ctx, k.Store, mrepo, rev); err == nil {
			res.Offline = true
		} else if !errors.Is(err, manifest.ErrNotFound) {
			return nil, err
		}
	}
	client := k.Hub
	if m == nil {
		info, client, err = k.repoInfo(ctx, req.Repo, rev)
		if err != nil {
			return nil, err
		}
		// The Hub may have canonicalised a legacy or mis-cased id.
		if err := hub.ValidateRepoID(info.ID); err == nil && info.ID != "" {
			req.Repo.ID = info.ID
			mrepo.ID = info.ID
		}
		m, err = manifest.Load(ctx, k.Store, mrepo, info.SHA)
		if errors.Is(err, manifest.ErrNotFound) {
			m, err = k.buildManifest(ctx, client, req.Repo, info)
			if err == nil {
				err = k.checkRegistry(ctx, client, req.Repo, m, res)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	res.Manifest = m

	var targets []fetch.Target
	for _, f := range m.Files {
		if f.LFS && (req.NoLFS || !selected(f.Path, include, exclude)) {
			res.Skipped = append(res.Skipped, f.Path)
			continue
		}
		t := fetch.Target{Repo: req.Repo, Commit: m.Commit, Path: f.Path, Size: f.Size, TreeOID: f.GitSHA1}
		if f.LFS {
			t.LFS, t.SHA256 = true, f.SHA256
		}
		if _, ok := k.Fetcher.Have(ctx, t); ok {
			res.Present = append(res.Present, f.Path)
			continue
		}
		if res.Offline {
			return nil, fmt.Errorf("%s is not kept locally; pull it by branch name or without the commit pin to fetch it", f.Path)
		}
		targets = append(targets, t)
	}

	if res.Offline {
		client = k.ClientFor(m.Upstream)
	} else if m.Upstream != client.Base() {
		// Kept earlier from another upstream; files come from where we
		// resolved the revision this time.
		m.Upstream = client.Base()
	}
	var failed []fetch.Result
	for _, r := range k.fetcherFor(client).Files(ctx, targets) {
		if r.Err != nil {
			failed = append(failed, r)
			continue
		}
		res.Downloaded = append(res.Downloaded, r.Target.Path)
	}

	// Regular files have no SHA-256 upstream; fill them in from the store.
	if err := k.fillSHA256(ctx, m); err != nil {
		return nil, err
	}
	if len(failed) > 0 {
		return res, &FileError{Failures: failed}
	}
	if !res.Offline {
		if err := manifest.Save(ctx, k.Store, m); err != nil {
			return nil, err
		}
		if info != nil && info.SHA == m.Commit {
			if err := manifest.SaveInfo(k.Store, mrepo, m.Commit, info.Raw); err != nil {
				return nil, err
			}
		}
		if err := manifest.SetRef(ctx, k.Store, mrepo, rev, m.Commit, k.now()); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// buildManifest lists the tree and records hashes the Hub gives us. SHA-256
// of regular files is filled in after they are downloaded.
func (k *Keeper) buildManifest(ctx context.Context, client *hub.Client, repo hub.Repo, info *hub.RepoInfo) (*manifest.Manifest, error) {
	tree, err := client.Tree(ctx, repo, info.SHA)
	if err != nil {
		return nil, err
	}
	m := &manifest.Manifest{
		Version:   manifest.FormatVersion,
		Repo:      manifest.Repo{Type: string(repo.Type), ID: repo.ID},
		Commit:    info.SHA,
		FetchedAt: k.now(),
		Upstream:  client.Base(),
		License: manifest.License{
			IDs:        info.CardData.License,
			Name:       info.CardData.LicenseName,
			Link:       info.CardData.LicenseLink,
			Gated:      string(info.Gated),
			BaseModels: info.CardData.BaseModel,
		},
	}
	for _, e := range tree {
		if !e.IsFile() {
			continue
		}
		f := manifest.File{Path: e.Path, Size: e.Size, GitSHA1: e.OID}
		if e.LFS != nil {
			f.LFS, f.SHA256, f.Size = true, e.LFS.OID, e.LFS.Size
		}
		m.Files = append(m.Files, f)
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, nil
}

func (k *Keeper) fillSHA256(ctx context.Context, m *manifest.Manifest) error {
	for i, f := range m.Files {
		if f.SHA256 != "" {
			continue
		}
		b, err := k.Store.LookupGitSHA1(ctx, f.GitSHA1)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue // failed download; reported separately
			}
			return err
		}
		m.Files[i].SHA256 = b.SHA256
	}
	return nil
}

func selected(path string, include, exclude []*regexp.Regexp) bool {
	if len(include) > 0 && !matchAny(path, include) {
		return false
	}
	return !matchAny(path, exclude)
}

func matchAny(path string, globs []*regexp.Regexp) bool {
	for _, g := range globs {
		if g.MatchString(path) {
			return true
		}
	}
	return false
}

// compileGlobs converts fnmatch-style patterns (as huggingface_hub's
// allow_patterns use them) to regexps. "*" matches any run of characters
// including "/", "?" one character, "[...]" a class. A pattern ending in
// "/" matches everything under that directory.
func compileGlobs(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if strings.HasSuffix(p, "/") {
			p += "*"
		}
		var b strings.Builder
		b.WriteString("^")
		for i := 0; i < len(p); i++ {
			switch c := p[i]; c {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteString(".")
			case '[':
				j := strings.IndexByte(p[i:], ']')
				if j < 0 {
					return nil, fmt.Errorf("pattern %q: unclosed [", p)
				}
				class := p[i+1 : i+j]
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}
				b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
				i += j
			default:
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		b.WriteString("$")
		re, err := regexp.Compile(b.String())
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// checkRegistry compares a revision just listed upstream with the registry.
// A different file list for the same commit is refused: git ids cover the
// content, so it means the upstream (often a mirror) is lying. A commit the
// registry lists for this repo that the upstream no longer has means its
// history was rewritten, or the repo was deleted and re-created under the
// same name; that is a warning, since the new content may be legitimate.
func (k *Keeper) checkRegistry(ctx context.Context, client *hub.Client, repo hub.Repo, m *manifest.Manifest, res *PullResult) error {
	if k.Registry == nil {
		return nil
	}
	rec, _, ok, err := k.Registry.Record(m.Repo, m.Commit)
	if err != nil {
		return err
	}
	if ok {
		if diff := manifest.Diff(rec, m); len(diff) > 0 {
			return fmt.Errorf("%w: %s serves different files for %s@%s than the registry recorded:\n  %s",
				ErrRegistryMismatch, client.Base(), m.Repo, m.Commit[:12], strings.Join(diff, "\n  "))
		}
		return nil
	}
	latest, _, ok, err := k.Registry.Latest(m.Repo)
	if err != nil || !ok {
		return err
	}
	if _, err := client.RepoInfo(ctx, repo, latest.Commit); errors.Is(err, hub.ErrRevisionNotFound) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"%s no longer has commit %s, which the registry lists for %s (fetched %s). "+
				"Its history was rewritten, or the repo was deleted and re-created under the same name: "+
				"check who publishes it now before trusting %s", client.Base(), latest.Commit[:12], m.Repo,
			latest.FetchedAt.Format("2006-01-02"), m.Commit[:12]))
	}
	return nil
}
