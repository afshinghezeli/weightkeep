package version

import (
	"runtime/debug"
	"testing"
)

func TestFillFromBuildInfo(t *testing.T) {
	tests := []struct {
		name  string
		start Info
		bi    debug.BuildInfo
		want  Info
	}{
		{
			name: "go install build",
			bi: debug.BuildInfo{
				Main: debug.Module{Version: "v0.2.1"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.time", Value: "2026-09-24T10:00:00Z"},
				},
			},
			want: Info{Version: "v0.2.1", Commit: "abc123", Date: "2026-09-24T10:00:00Z"},
		},
		{
			name:  "ldflags win over build info",
			start: Info{Version: "v1.0.0", Commit: "fromldflags"},
			bi: debug.BuildInfo{
				Main:     debug.Module{Version: "v0.9.0"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "other"}},
			},
			want: Info{Version: "v1.0.0", Commit: "fromldflags"},
		},
		{
			name: "local build with dirty tree",
			bi: debug.BuildInfo{
				Main:     debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}},
			},
			want: Info{Modified: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.start
			fillFromBuildInfo(&got, &tt.bi)
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []struct {
		in   Info
		want string
	}{
		{
			in:   Info{Version: "v0.1.0", Commit: "0123456789abcdef", Date: "2026-09-24", GoVersion: "go1.27.1", Platform: "linux/amd64"},
			want: "weightkeep v0.1.0 (0123456789ab, 2026-09-24) go1.27.1 linux/amd64",
		},
		{
			in:   Info{Version: "dev", GoVersion: "go1.27.1", Platform: "darwin/arm64", Modified: true},
			want: "weightkeep dev (unknown-dirty) go1.27.1 darwin/arm64",
		},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}
