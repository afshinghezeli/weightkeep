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
	Now     func() time.Time
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
	if m == nil {
		info, err = k.Hub.RepoInfo(ctx, req.Repo, rev)
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
			m, err = k.buildManifest(ctx, req.Repo, info)
		}
		if err != nil {
			return nil, err
		}
	}
	res.Manifest = m

	var targets []fetch.Target
	for _, f := range m.Files {
		if f.LFS && !selected(f.Path, include, exclude) {
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

	var failed []fetch.Result
	for _, r := range k.Fetcher.Files(ctx, targets) {
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
		if err := manifest.SetRef(ctx, k.Store, mrepo, rev, m.Commit, k.now()); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// buildManifest lists the tree and records hashes the Hub gives us. SHA-256
// of regular files is filled in after they are downloaded.
func (k *Keeper) buildManifest(ctx context.Context, repo hub.Repo, info *hub.RepoInfo) (*manifest.Manifest, error) {
	tree, err := k.Hub.Tree(ctx, repo, info.SHA)
	if err != nil {
		return nil, err
	}
	m := &manifest.Manifest{
		Version:   manifest.FormatVersion,
		Repo:      manifest.Repo{Type: string(repo.Type), ID: repo.ID},
		Commit:    info.SHA,
		FetchedAt: k.now(),
		Upstream:  k.Hub.Base(),
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
