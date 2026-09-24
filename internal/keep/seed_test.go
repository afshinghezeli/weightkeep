package keep

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/policy"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

func TestSeedPlan(t *testing.T) {
	k, h := newKeeper(t)
	ctx := context.Background()
	mit, _ := policy.Text("MIT")
	h.Add(&fakehub.Repo{ID: "acme/mit", License: "mit", Files: []fakehub.File{{Path: "LICENSE", Content: []byte(mit)}}})
	h.Add(&fakehub.Repo{ID: "acme/nc", License: "cc-by-nc-4.0", Files: []fakehub.File{{Path: "LICENSE", Content: []byte("Attribution-NonCommercial 4.0 International")}}})
	h.Add(&fakehub.Repo{ID: "acme/unlicensed", Files: []fakehub.File{{Path: "config.json", Content: []byte("{}")}}})
	h.Add(&fakehub.Repo{ID: "acme/partial", License: "mit", Files: []fakehub.File{
		{Path: "a.gguf", Content: []byte(strings.Repeat("a", 3000)), LFS: true},
		{Path: "b.gguf", Content: []byte(strings.Repeat("b", 3000)), LFS: true},
	}})
	for _, id := range []string{"acme/mit", "acme/nc", "acme/unlicensed"} {
		if _, err := k.Pull(ctx, PullRequest{Repo: hub.Repo{Type: hub.Model, ID: id}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.Pull(ctx, PullRequest{Repo: hub.Repo{Type: hub.Model, ID: "acme/partial"}, Include: []string{"a.gguf"}}); err != nil {
		t.Fatal(err)
	}

	plan := func(opt SeedOptions) map[string]SeedCandidate {
		cs, err := k.SeedPlan(ctx, Selector{}, opt)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]SeedCandidate{}
		for _, c := range cs {
			out[c.Manifest.Repo.ID] = c
		}
		return out
	}
	p := plan(SeedOptions{Online: true})
	if !p["acme/mit"].Seed {
		t.Errorf("tier A not seeded: %s", p["acme/mit"].Why)
	}
	if c := p["acme/unlicensed"]; c.Seed || !strings.Contains(c.Why, "tier C") || !strings.Contains(c.Why, "no licence declared") {
		t.Errorf("unlicensed: seed=%v why=%q", c.Seed, c.Why)
	}
	if c := p["acme/nc"]; c.Seed || !strings.Contains(c.Why, "--allow acme/nc --non-commercial") {
		t.Errorf("B2 without opt-in: seed=%v why=%q", c.Seed, c.Why)
	}
	if c := p["acme/partial"]; c.Seed || !strings.Contains(c.Why, "only 1 of 2 files") {
		t.Errorf("partial: seed=%v why=%q", c.Seed, c.Why)
	}
	if c := plan(SeedOptions{Online: true, Allow: []string{"acme/nc"}})["acme/nc"]; c.Seed {
		t.Error("B2 seeded without the non-commercial statement")
	}
	if c := plan(SeedOptions{Online: true, Allow: []string{"acme/nc"}, NonCommercial: true})["acme/nc"]; !c.Seed {
		t.Errorf("B2 with both opt-ins not seeded: %s", c.Why)
	}
}

func TestUploadedPerMonth(t *testing.T) {
	k, _ := newKeeper(t)
	ctx := context.Background()
	m := Month(time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC))
	if m != "2026-09" {
		t.Fatalf("Month = %s", m)
	}
	if n, _ := Uploaded(ctx, k.Store, m); n != 0 {
		t.Errorf("fresh month = %d", n)
	}
	if _, err := AddUploaded(ctx, k.Store, m, 100); err != nil {
		t.Fatal(err)
	}
	if n, err := AddUploaded(ctx, k.Store, m, 50); err != nil || n != 150 {
		t.Errorf("total = %d, %v", n, err)
	}
	if n, _ := Uploaded(ctx, k.Store, "2026-10"); n != 0 {
		t.Errorf("next month = %d", n)
	}
}

type denyRepo string

func (d denyRepo) Denied(m *manifest.Manifest) (string, bool) {
	return "DMCA notice", m.Repo.ID == string(d)
}

func TestSeedPlanHonoursDenylist(t *testing.T) {
	k, h := newKeeper(t)
	ctx := context.Background()
	mit, _ := policy.Text("MIT")
	h.Add(&fakehub.Repo{ID: "acme/mit", License: "mit", Files: []fakehub.File{{Path: "LICENSE", Content: []byte(mit)}}})
	if _, err := k.Pull(ctx, PullRequest{Repo: hub.Repo{Type: hub.Model, ID: "acme/mit"}}); err != nil {
		t.Fatal(err)
	}
	k.Deny = denyRepo("acme/mit")
	cs, err := k.SeedPlan(ctx, Selector{}, SeedOptions{Online: true})
	if err != nil || len(cs) != 1 {
		t.Fatalf("plan: %v %v", cs, err)
	}
	if c := cs[0]; c.Seed || !strings.Contains(c.Why, "denylist: DMCA notice") {
		t.Errorf("denylisted tier A revision: seed=%v why=%q", c.Seed, c.Why)
	}
}
