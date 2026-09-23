package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
