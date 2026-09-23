package proxy

import (
	"testing"

	"github.com/afshinghezeli/weightkeep/internal/hub"
)

func TestParseRoute(t *testing.T) {
	model := func(id string) hub.Repo { return hub.Repo{Type: hub.Model, ID: id} }
	tests := []struct {
		in   string
		want route
	}{
		{"/acme/tiny/resolve/main/config.json", route{routeResolve, model("acme/tiny"), "main", "config.json"}},
		{"/gpt2/resolve/main/config.json", route{routeResolve, model("gpt2"), "main", "config.json"}},
		{"/acme/tiny/resolve/main/onnx/model.onnx", route{routeResolve, model("acme/tiny"), "main", "onnx/model.onnx"}},
		{"/acme/tiny/resolve/refs%2Fpr%2F3/config.json", route{routeResolve, model("acme/tiny"), "refs/pr/3", "config.json"}},
		{"/acme/tiny/resolve/refs/pr/3/config.json", route{routeResolve, model("acme/tiny"), "refs/pr/3", "config.json"}},
		{"/acme/tiny/resolve/main/with%20space.txt", route{routeResolve, model("acme/tiny"), "main", "with space.txt"}},
		{"/datasets/acme/data/resolve/main/train.parquet", route{routeResolve, hub.Repo{Type: hub.Dataset, ID: "acme/data"}, "main", "train.parquet"}},
		{"/api/models/acme/tiny", route{routeInfo, model("acme/tiny"), "main", ""}},
		{"/api/models/gpt2", route{routeInfo, model("gpt2"), "main", ""}},
		{"/api/models/acme/tiny/revision/main", route{routeInfo, model("acme/tiny"), "main", ""}},
		{"/api/models/acme/tiny/revision/refs%2Fpr%2F1", route{routeInfo, model("acme/tiny"), "refs/pr/1", ""}},
		{"/api/models/acme/tiny/tree/main", route{routeTree, model("acme/tiny"), "main", ""}},
		{"/api/models/acme/tiny/tree/main/onnx", route{routeTree, model("acme/tiny"), "main", "onnx"}},
		{"/api/models/acme/tiny/tree/main/onnx%2Fsub", route{routeTree, model("acme/tiny"), "main", "onnx/sub"}},
		{"/api/models/acme/tiny/refs", route{routeRefs, model("acme/tiny"), "", ""}},
		{"/api/models/acme/tiny/paths-info/main", route{routePathsInfo, model("acme/tiny"), "main", ""}},
		{"/api/datasets/acme/data/tree/main", route{routeTree, hub.Repo{Type: hub.Dataset, ID: "acme/data"}, "main", ""}},
		{"/api/whoami-v2", route{kind: routeAPIOther}},
		{"/api/models/acme/tiny/commits/main", route{kind: routeAPIOther}},
		{"/", route{}},
		{"/favicon.ico", route{}},
	}
	for _, tt := range tests {
		got, err := parseRoute(tt.in)
		if err != nil {
			t.Errorf("parseRoute(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseRoute(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseRouteRejectsHostilePaths(t *testing.T) {
	for _, in := range []string{
		"/acme/tiny/resolve/main/../../etc/passwd",
		"/acme/tiny/resolve/main/%2E%2E/%2E%2E/etc/passwd",
		"/acme/tiny/resolve/main/a%2F..%2F..%2Fx",
		"/acme/tiny/resolve/main/",
		"/../resolve/main/x",
		"/acme%2F..%2Fx/resolve/main/x",
		"/a/b/c/resolve/main/x",
		"/api/models/acme/tiny/tree/main/..%2F..",
		"/api/models/..%2Fetc/tree/main",
	} {
		if r, err := parseRoute(in); err == nil && (r.kind == routeResolve || r.kind == routeTree) {
			t.Errorf("parseRoute(%q) accepted: %+v", in, r)
		}
	}
}
