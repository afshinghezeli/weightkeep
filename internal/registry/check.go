package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/policy"
)

// maxSmallFile bounds how much of a non-LFS file Check downloads to hash it.
// git stores anything bigger in LFS on the Hub, so this is generous.
const maxSmallFile = 10 << 20

// Check verifies a submitted record against the Hub before it is signed
// into the registry: the commit exists, every file matches what the Hub
// serves, the repo isn't gated or private, the licence allows sharing
// (tier A or B) and nothing is denylisted. It returns the licence decision
// so the reviewer can see the tier.
func Check(ctx context.Context, c *hub.Client, r *Record, deny *Denylist) (policy.Decision, error) {
	if err := r.Validate(); err != nil {
		return policy.Decision{}, err
	}
	m := r.Manifest
	if e, ok := deny.Match(m); ok {
		return policy.Decision{}, fmt.Errorf("%s@%s is on the denylist: %s", m.Repo, m.Commit, e.Reason)
	}
	repo, err := hub.ParseRepo(m.Repo.String())
	if err != nil {
		return policy.Decision{}, err
	}
	info, err := c.RepoInfo(ctx, repo, m.Commit)
	if err != nil {
		return policy.Decision{}, err
	}
	if info.SHA != m.Commit {
		return policy.Decision{}, fmt.Errorf("the Hub resolves %s to %s, not %s", m.Commit, info.SHA, m.Commit)
	}
	if info.Gated.IsGated() || info.Private {
		return policy.Decision{Tier: policy.C}, fmt.Errorf("%s is gated or private, so it is tier C and can't be listed", m.Repo)
	}
	tree, err := c.Tree(ctx, repo, m.Commit)
	if err != nil {
		return policy.Decision{}, err
	}
	licence, err := compareFiles(ctx, c, repo, m, tree)
	if err != nil {
		return policy.Decision{}, err
	}

	in := policy.Input{
		LicenseIDs:  info.CardData.License,
		LicenseName: info.CardData.LicenseName,
		Gated:       string(info.Gated),
		Private:     info.Private,
	}
	if licence != nil {
		in.LicenseFile, in.LicenseFilePath = &licence.text, licence.path
	}
	for i, id := range info.CardData.BaseModel {
		if i == 5 {
			break
		}
		b := policy.Base{ID: id}
		if base, err := hub.ParseRepo(id); err == nil {
			if bi, err := c.RepoInfo(ctx, base, "main"); err == nil {
				b.Tier = policy.TierOf(bi.CardData.License, bi.CardData.LicenseName, string(bi.Gated), bi.Private)
				b.Known = true
			}
		}
		in.Bases = append(in.Bases, b)
	}
	d := policy.Evaluate(in)
	if d.Tier == policy.C {
		return d, fmt.Errorf("%s@%s is tier C and can't be listed: %s", m.Repo, m.Commit[:12], strings.Join(d.Reasons, "; "))
	}
	return d, nil
}

type licenceFile struct{ path, text string }

// compareFiles checks the manifest lists exactly the Hub's files with the
// same sizes and hashes. LFS files are compared by the SHA-256 the Hub
// lists; regular files are small, so they are downloaded and hashed.
func compareFiles(ctx context.Context, c *hub.Client, repo hub.Repo, m *manifest.Manifest, tree []hub.TreeEntry) (*licenceFile, error) {
	want := map[string]manifest.File{}
	for _, f := range m.Files {
		want[f.Path] = f
	}
	var problems []string
	var licence *licenceFile
	for _, e := range tree {
		if !e.IsFile() {
			continue
		}
		f, ok := want[e.Path]
		delete(want, e.Path)
		if !ok {
			problems = append(problems, e.Path+": on the Hub but not in the record")
			continue
		}
		if f.GitSHA1 != e.OID {
			problems = append(problems, fmt.Sprintf("%s: git id %s, the Hub has %s", e.Path, f.GitSHA1, e.OID))
			continue
		}
		if e.LFS != nil {
			if f.SHA256 != e.LFS.OID || f.Size != e.LFS.Size {
				problems = append(problems, fmt.Sprintf("%s: sha256 %s (%d bytes), the Hub has %s (%d bytes)", e.Path, f.SHA256, f.Size, e.LFS.OID, e.LFS.Size))
			}
			continue
		}
		data, err := small(ctx, c, repo, m.Commit, e.Path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != f.SHA256 || int64(len(data)) != f.Size {
			problems = append(problems, fmt.Sprintf("%s: sha256 %s (%d bytes), the Hub serves %s (%d bytes)", e.Path, f.SHA256, f.Size, got, len(data)))
			continue
		}
		if licence == nil && policy.IsLicenceFile(e.Path) {
			licence = &licenceFile{path: e.Path, text: string(data)}
		}
	}
	for p := range want {
		problems = append(problems, p+": in the record but not on the Hub")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the record doesn't match the Hub at %s:\n  %s", m.Commit[:12], strings.Join(problems, "\n  "))
	}
	return licence, nil
}

func small(ctx context.Context, c *hub.Client, repo hub.Repo, commit, path string) ([]byte, error) {
	body, err := c.Download(ctx, repo, commit, path, 0)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxSmallFile+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(data) > maxSmallFile {
		return nil, errors.New(path + ": regular (non-LFS) file over 10 MiB")
	}
	return data, nil
}
