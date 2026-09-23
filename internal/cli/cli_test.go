package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
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
