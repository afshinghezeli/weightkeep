package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/config"
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
