package keep

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

type fakeRegistry struct {
	records map[string]*manifest.Manifest // "repo@commit"
	latest  *manifest.Manifest
}

func (r *fakeRegistry) Record(repo manifest.Repo, commit string) (*manifest.Manifest, string, bool, error) {
	m, ok := r.records[repo.String()+"@"+commit]
	return m, "magnet:?xt=urn:btih:" + strings.Repeat("a", 40), ok, nil
}

func (r *fakeRegistry) Latest(manifest.Repo) (*manifest.Manifest, string, bool, error) {
	return r.latest, "", r.latest != nil, nil
}

func clone(m *manifest.Manifest) *manifest.Manifest {
	c := *m
	c.Files = append([]manifest.File(nil), m.Files...)
	return &c
}

func TestPullChecksTheRegistry(t *testing.T) {
	ctx := context.Background()
	a, _ := newKeeper(t)
	res, err := a.Pull(ctx, PullRequest{Repo: tinyRep, NoLFS: true})
	if err != nil {
		t.Fatal(err)
	}
	good := res.Manifest
	key := good.Repo.String() + "@" + good.Commit

	// The record matches: nothing to say.
	b, _ := newKeeper(t)
	b.Registry = &fakeRegistry{records: map[string]*manifest.Manifest{key: good}, latest: good}
	res, err = b.Pull(ctx, PullRequest{Repo: tinyRep, NoLFS: true})
	if err != nil || len(res.Warnings) > 0 {
		t.Fatalf("matching record: %v %v", err, res.Warnings)
	}

	// Upstream lists different content for the same commit than the
	// registry recorded: refused, and nothing is recorded.
	recorded := clone(good)
	recorded.Files[0].GitSHA1 = strings.Repeat("f", 40)
	c, _ := newKeeper(t)
	c.Registry = &fakeRegistry{records: map[string]*manifest.Manifest{key: recorded}}
	_, err = c.Pull(ctx, PullRequest{Repo: tinyRep, NoLFS: true})
	if !errors.Is(err, ErrRegistryMismatch) || !strings.Contains(err.Error(), recorded.Files[0].Path) {
		t.Fatalf("contradicting upstream: %v", err)
	}
	if _, err := manifest.Load(ctx, c.Store, good.Repo, good.Commit); !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("manifest saved despite the mismatch: %v", err)
	}

	// The registry knows a commit upstream no longer has: the repo was
	// rewritten or re-created. Pull goes ahead, with a warning.
	gone := clone(good)
	gone.Commit = strings.Repeat("1", 40)
	d, _ := newKeeper(t)
	d.Registry = &fakeRegistry{latest: gone}
	res, err = d.Pull(ctx, PullRequest{Repo: tinyRep, NoLFS: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no longer has commit 111111111111") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}
