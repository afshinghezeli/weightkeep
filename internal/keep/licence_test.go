package keep

import (
	"context"
	"strings"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/hub"
	"github.com/afshinghezeli/weightkeep/internal/policy"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

func TestLicence(t *testing.T) {
	k, h := newKeeper(t)
	ctx := context.Background()
	mit, _ := policy.Text("MIT")
	h.Add(&fakehub.Repo{ID: "acme/mit", License: "mit", Files: []fakehub.File{{Path: "LICENSE", Content: []byte(mit)}}})
	h.Add(&fakehub.Repo{ID: "acme/liar", License: "mit", Files: []fakehub.File{{Path: "LICENSE", Content: []byte("All rights reserved. No redistribution.")}}})
	h.Add(&fakehub.Repo{ID: "acme/gated-base", License: "mit", Gated: "manual"})
	h.Add(&fakehub.Repo{ID: "acme/finetune", License: "mit", BaseModel: "acme/gated-base"})
	h.Add(&fakehub.Repo{ID: "acme/orphan", License: "mit", BaseModel: "acme/deleted-base"})

	check := func(id string, want policy.Tier, reason string) {
		t.Helper()
		res, err := k.Pull(ctx, PullRequest{Repo: hub.Repo{Type: hub.Model, ID: id}})
		if err != nil {
			t.Fatal(err)
		}
		d, err := k.Licence(ctx, res.Manifest, true)
		if err != nil {
			t.Fatal(err)
		}
		if d.Tier != want || !strings.Contains(strings.Join(d.Reasons, ";"), reason) {
			t.Errorf("%s: tier %s reasons %q; want %s mentioning %q", id, d.Tier, d.Reasons, want, reason)
		}
	}
	check("acme/mit", policy.A, "")
	check("acme/liar", policy.C, "does not look like")
	// A fine-tune of a gated model inherits the gate.
	check("acme/finetune", policy.C, "base model acme/gated-base is tier C")
	check("acme/orphan", policy.C, "could not be checked")
	// The shared fixture's LICENSE is a stub ("Apache License 2.0 ...")
	// without the full text, so it doesn't count as the Apache licence.
	check(tinyRep.ID, policy.C, "does not look like")
}
