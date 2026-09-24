package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/config"
	"github.com/afshinghezeli/weightkeep/internal/testutil/fakehub"
)

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Execute(context.Background(), Streams{In: strings.NewReader(""), Out: &out, Err: &errOut}, args)
	return out.String(), errOut.String(), code
}

func TestVersion(t *testing.T) {
	out, _, code := run(t, "version")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out, "weightkeep ") {
		t.Errorf("unexpected output %q", out)
	}
}

func TestVersionJSON(t *testing.T) {
	out, _, code := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"version", "go", "platform"} {
		if _, ok := v[k]; !ok {
			t.Errorf("missing key %q in %s", k, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	_, errOut, code := run(t, "frobnicate")
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.HasPrefix(errOut, "weightkeep: unknown command") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestEnvNeverPrintsToken(t *testing.T) {
	d := deps{loadConfig: func() (*config.Config, error) {
		return &config.Config{
			Home:        "/data/weightkeep",
			Upstream:    "https://huggingface.co",
			Token:       config.NewToken("hf_supersecret"),
			TokenSource: "HF_TOKEN",
		}, nil
	}}
	for _, args := range [][]string{{"env"}, {"env", "--json"}} {
		var out, errOut bytes.Buffer
		code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, args)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
		}
		if strings.Contains(out.String(), "supersecret") {
			t.Errorf("%v leaks the token:\n%s", args, out.String())
		}
		if !strings.Contains(out.String(), "hf_*** (from HF_TOKEN)") {
			t.Errorf("%v does not say where the token came from:\n%s", args, out.String())
		}
	}
}

func TestPull(t *testing.T) {
	h := fakehub.New(&fakehub.Repo{ID: "acme/tiny", License: "mit", Files: []fakehub.File{
		{Path: "config.json", Content: []byte("{}")},
		{Path: "model.safetensors", Content: bytes.Repeat([]byte("w"), 5000), LFS: true},
	}})
	defer h.Close()
	home := t.TempDir()
	d := deps{loadConfig: func() (*config.Config, error) {
		return &config.Config{Home: home, Upstream: h.URL}, nil
	}}

	var out, errOut bytes.Buffer
	if code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, []string{"pull", "acme/tiny"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("pull wrote to stdout: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "kept acme/tiny@") || !strings.Contains(errOut.String(), "2 downloaded") {
		t.Errorf("summary missing: %s", errOut.String())
	}

	errOut.Reset()
	if code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, []string{"pull", "acme/tiny"}); code != 0 {
		t.Fatalf("second pull exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "0 downloaded, 2 already kept") {
		t.Errorf("second pull summary: %s", errOut.String())
	}

	errOut.Reset()
	code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, []string{"pull", "acme/missing"})
	if code == 0 || !strings.Contains(errOut.String(), "check the spelling") {
		t.Errorf("missing repo: exit %d, %s", code, errOut.String())
	}
	code = execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, []string{"pull", "../etc"})
	if code == 0 {
		t.Error("invalid repo id accepted")
	}
}

func TestLsVerifyRepair(t *testing.T) {
	weights := fakehub.File{Path: "model.safetensors", Content: bytes.Repeat([]byte("w"), 5000), LFS: true}
	h := fakehub.New(&fakehub.Repo{ID: "acme/tiny", Files: []fakehub.File{{Path: "config.json", Content: []byte("{}")}, weights}})
	defer h.Close()
	home := t.TempDir()
	d := deps{loadConfig: func() (*config.Config, error) { return &config.Config{Home: home, Upstream: h.URL}, nil }}
	run := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, args)
		return out.String(), errOut.String(), code
	}

	if _, errOut, _ := run("ls"); !strings.Contains(errOut, "nothing kept yet") {
		t.Errorf("empty ls: %q", errOut)
	}
	if _, errOut, code := run("pull", "acme/tiny"); code != 0 {
		t.Fatal(errOut)
	}
	out, _, _ := run("ls")
	if !strings.Contains(out, "acme/tiny") || !strings.Contains(out, "2/2") {
		t.Errorf("ls: %s", out)
	}
	out, _, _ = run("ls", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0]["kept_files"] != float64(2) {
		t.Errorf("ls --json: %s (%v)", out, err)
	}

	if out, errOut, code := run("verify", "acme/tiny@main"); code != 0 || !strings.Contains(out, "ok ") {
		t.Fatalf("verify: %d %s %s", code, out, errOut)
	}

	// Corrupt the weights blob.
	blob := filepath.Join(home, "blobs", "sha256", weights.SHA256()[:2], weights.SHA256())
	if err := os.Chmod(blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, bytes.Repeat([]byte("x"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := run("verify")
	if code == 0 || !strings.Contains(out, "CORRUPT") || !strings.Contains(out, "model.safetensors") {
		t.Fatalf("verify after corruption: exit %d\n%s", code, out)
	}
	// Quarantined, so the next pull fetches a clean copy and verify passes.
	if _, errOut, code := run("pull", "acme/tiny"); code != 0 || !strings.Contains(errOut, "1 downloaded") {
		t.Fatalf("re-pull: %d %s", code, errOut)
	}
	if out, _, code := run("verify"); code != 0 {
		t.Fatalf("verify after repair: %d %s", code, out)
	}
	if _, _, code := run("verify", "acme/other"); code == 0 {
		t.Error("verify of an unknown repo should fail")
	}
}

func TestRmAndGC(t *testing.T) {
	h := fakehub.New(&fakehub.Repo{ID: "acme/tiny", Files: []fakehub.File{
		{Path: "config.json", Content: []byte("{}")},
		{Path: "model.safetensors", Content: bytes.Repeat([]byte("w"), 5000), LFS: true},
	}})
	defer h.Close()
	home := t.TempDir()
	d := deps{loadConfig: func() (*config.Config, error) { return &config.Config{Home: home, Upstream: h.URL}, nil }}
	run := func(args ...string) (string, int) {
		var out, errOut bytes.Buffer
		code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, args)
		return out.String() + errOut.String(), code
	}
	if out, code := run("pull", "acme/tiny"); code != 0 {
		t.Fatal(out)
	}
	if out, code := run("rm", "acme/tiny"); code == 0 || !strings.Contains(out, "acme/tiny@main") {
		t.Errorf("rm without a revision: %d %s", code, out)
	}
	if out, code := run("rm", "acme/tiny@main"); code != 0 || !strings.Contains(out, "forgot acme/tiny@") {
		t.Fatalf("rm: %d %s", code, out)
	}
	if out, code := run("gc", "--grace", "0s"); code != 0 || !strings.Contains(out, "removed 2 blob(s)") {
		t.Errorf("gc: %d %s", code, out)
	}
	if out, _ := run("ls"); !strings.Contains(out, "nothing kept yet") {
		t.Errorf("ls after rm: %s", out)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "500": 500, "10MB": 10e6, "1.5GB": 1.5e9, "2TiB": 2 << 40, "64 kib": 64 << 10} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ten", "-1MB", "5XB"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted", bad)
		}
	}
}

func TestSeedDryRunNamesTheRule(t *testing.T) {
	h := fakehub.New(
		&fakehub.Repo{ID: "acme/open", License: "mit", Files: []fakehub.File{{Path: "config.json", Content: []byte("{}")}}},
		&fakehub.Repo{ID: "acme/closed", Files: []fakehub.File{{Path: "config.json", Content: []byte("[]")}}},
	)
	defer h.Close()
	home := t.TempDir()
	d := deps{loadConfig: func() (*config.Config, error) { return &config.Config{Home: home, Upstream: h.URL}, nil }}
	run := func(args ...string) (string, int) {
		var out, errOut bytes.Buffer
		code := execute(context.Background(), Streams{Out: &out, Err: &errOut}, d, args)
		return out.String() + errOut.String(), code
	}
	for _, r := range []string{"acme/open", "acme/closed"} {
		if out, code := run("pull", r); code != 0 {
			t.Fatal(out)
		}
	}
	out, code := run("seed", "--dry-run")
	if code != 0 {
		t.Fatal(out)
	}
	if !strings.Contains(out, "share  acme/open@") {
		t.Errorf("MIT repo not shared:\n%s", out)
	}
	if !strings.Contains(out, "skip   acme/closed@") || !strings.Contains(out, "tier C, never shared: no licence declared") {
		t.Errorf("unlicensed repo not refused with its rule:\n%s", out)
	}
	if out, code := run("seed", "--dry-run", "acme/closed"); code == 0 || !strings.Contains(out, "nothing to share") {
		t.Errorf("seeding only a tier C repo: %d\n%s", code, out)
	}
}
