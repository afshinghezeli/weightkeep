package keep

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/policy"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// SeedOptions says what the operator allows beyond the defaults.
type SeedOptions struct {
	// Allow lists repos whose tier B licence the operator has read and
	// opted in to sharing ("org/name").
	Allow []string
	// NonCommercial is the operator's statement that this node shares for
	// non-commercial purposes, required for tier B2.
	NonCommercial bool
	// Online looks up base models on the Hub.
	Online bool
}

// SeedCandidate is one kept revision and whether it may be seeded.
type SeedCandidate struct {
	Manifest *manifest.Manifest
	Decision policy.Decision
	Seed     bool
	// Why explains a refusal, naming the rule.
	Why string
}

// SeedPlan decides, for every selected kept revision, whether it may be
// seeded. sel filters revisions as for Verify.
func (k *Keeper) SeedPlan(ctx context.Context, sel Selector, opt SeedOptions) ([]SeedCandidate, error) {
	sums, err := manifest.List(ctx, k.Store)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, a := range opt.Allow {
		allowed[a] = true
	}
	var out []SeedCandidate
	for _, sum := range sums {
		if !sel.matches(sum) {
			continue
		}
		m, err := manifest.Load(ctx, k.Store, sum.Repo, sum.Commit)
		if err != nil {
			return nil, err
		}
		d, err := k.Licence(ctx, m, opt.Online)
		if err != nil {
			return nil, err
		}
		c := SeedCandidate{Manifest: m, Decision: d}
		switch {
		case sum.KeptFiles < sum.Files:
			c.Why = fmt.Sprintf("only %d of %d files are kept; pull the rest first", sum.KeptFiles, sum.Files)
		case d.Tier == policy.C:
			c.Why = "tier C, never shared: " + firstReason(d)
		case d.Tier == policy.B1 && !allowed[m.Repo.ID]:
			c.Why = fmt.Sprintf("tier B1 (%s): read the licence, then opt in with --allow %s", d.License, m.Repo.ID)
		case d.Tier == policy.B2 && (!allowed[m.Repo.ID] || !opt.NonCommercial):
			c.Why = fmt.Sprintf("tier B2 (%s): non-commercial only; opt in with --allow %s --non-commercial", d.License, m.Repo.ID)
		default:
			c.Seed = true
		}
		out = append(out, c)
	}
	return out, nil
}

func firstReason(d policy.Decision) string {
	if len(d.Reasons) == 0 {
		return "no reason recorded"
	}
	return d.Reasons[0]
}

// Month is the key the monthly upload cap is tracked under.
func Month(t time.Time) string { return t.UTC().Format("2006-01") }

// AddUploaded records bytes uploaded this month and returns the new total.
func AddUploaded(ctx context.Context, st *store.Store, month string, n int64) (int64, error) {
	var total int64
	err := st.DB().QueryRowContext(ctx, `
		INSERT INTO seed_stats (month, uploaded) VALUES (?, ?)
		ON CONFLICT (month) DO UPDATE SET uploaded = uploaded + excluded.uploaded
		RETURNING uploaded`, month, n).Scan(&total)
	return total, err
}

// Uploaded returns the bytes uploaded in month.
func Uploaded(ctx context.Context, st *store.Store, month string) (int64, error) {
	var total int64
	err := st.DB().QueryRowContext(ctx, `SELECT uploaded FROM seed_stats WHERE month = ?`, month).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return total, err
}
