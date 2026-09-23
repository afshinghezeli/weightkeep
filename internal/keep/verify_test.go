package keep

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

func TestVerify(t *testing.T) {
	k, _ := newKeeper(t)
	ctx := context.Background()
	res, err := k.Pull(ctx, PullRequest{Repo: tinyRep, Include: []string{"*Q4*"}})
	if err != nil {
		t.Fatal(err)
	}

	var results []VerifyResult
	collect := func(r VerifyResult) { results = append(results, r) }
	if err := k.Verify(ctx, Selector{}, collect); err != nil {
		t.Fatal(err)
	}
	// config, LICENSE, the Q4 file and its byte-identical twin (same blob);
	// the other two were never downloaded.
	if len(results) != 4 {
		t.Fatalf("verified %d files, want 4: %+v", len(results), results)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: %v", r.File.Path, r.Err)
		}
	}

	// Flip a byte in the Q4 blob.
	f, _ := res.Manifest.File("model-Q4_K_M.gguf")
	path := k.Store.Path(f.SHA256)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	data[100] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	results = nil
	collect = func(r VerifyResult) {
		results = append(results, r)
		if IsCorrupt(r.Err) && k.Store.Has(r.File.SHA256) {
			if _, err := k.Store.Quarantine(r.File.SHA256); err != nil {
				t.Fatal(err)
			}
		}
	}
	sel := Selector{Repo: manifest.Repo{Type: "model", ID: tinyRep.ID}, Commit: res.Manifest.Commit}
	if err := k.Verify(ctx, sel, collect); err != nil {
		t.Fatal(err)
	}
	// collect quarantines on the first report, as the CLI does; the twin
	// sharing the blob must still be reported.
	var corrupt []string
	for _, r := range results {
		if IsCorrupt(r.Err) {
			corrupt = append(corrupt, r.File.Path)
		}
	}
	if len(corrupt) != 2 {
		t.Errorf("corrupt = %v, want both paths that share the blob", corrupt)
	}

	err = k.Verify(ctx, Selector{Repo: manifest.Repo{Type: "model", ID: "acme/other"}}, collect)
	if !errors.Is(err, manifest.ErrNotFound) {
		t.Errorf("unknown repo: %v", err)
	}
}
