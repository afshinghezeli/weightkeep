package keep

import (
	"context"
	"fmt"
	"io"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/policy"
)

// maxBases bounds how many base models are looked up per revision.
const maxBases = 5

// Licence decides the sharing tier of a kept revision. With online set, the
// base models' licences are looked up on the Hub; offline they count as
// unknown, which makes the decision conservative.
func (k *Keeper) Licence(ctx context.Context, m *manifest.Manifest, online bool) (policy.Decision, error) {
	in := policy.Input{
		LicenseIDs:  m.License.IDs,
		LicenseName: m.License.Name,
		Gated:       m.License.Gated,
	}
	in.DenyReason, in.Denylisted = k.Denied(m)
	for _, f := range m.Files {
		if !policy.IsLicenceFile(f.Path) {
			continue
		}
		text, err := k.readSmall(f)
		if err != nil {
			return policy.Decision{}, fmt.Errorf("read %s: %w", f.Path, err)
		}
		in.LicenseFile, in.LicenseFilePath = &text, f.Path
		break
	}
	for i, id := range m.License.BaseModels {
		if i == maxBases {
			break
		}
		b := policy.Base{ID: id}
		if online && k.Hub != nil {
			if repo, err := hub.ParseRepo(id); err == nil {
				if info, err := k.Hub.RepoInfo(ctx, repo, "main"); err == nil {
					b.Tier = policy.TierOf(info.CardData.License, info.CardData.LicenseName, string(info.Gated), info.Private)
					b.Known = true
				}
			}
		}
		in.Bases = append(in.Bases, b)
	}
	return policy.Evaluate(in), nil
}

func (k *Keeper) readSmall(f manifest.File) (string, error) {
	file, err := k.Store.Open(f.SHA256)
	if err != nil {
		return "", err
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 1<<20))
	return string(b), err
}
