// Package version reports what build of weightkeep is running.
//
// Release builds set Version, Commit and Date with -ldflags. Builds made
// with `go install` or `go build` fall back to the module and VCS data the
// Go toolchain embeds.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set by the linker: -X github.com/afshinghezeli/weightkeep/internal/version.Version=v0.1.0
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Info describes a build.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
	Platform  string `json:"platform"`
	Modified  bool   `json:"modified,omitempty"`
}

// Get returns the build information, filling gaps from debug.ReadBuildInfo.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		fillFromBuildInfo(&info, bi)
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

func fillFromBuildInfo(info *Info, bi *debug.BuildInfo) {
	if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
}

// String formats the info the way `weightkeep version` prints it.
func (i Info) String() string {
	commit := i.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if commit == "" {
		commit = "unknown"
	}
	if i.Modified {
		commit += "-dirty"
	}
	s := fmt.Sprintf("weightkeep %s (%s", i.Version, commit)
	if i.Date != "" {
		s += ", " + i.Date
	}
	return s + fmt.Sprintf(") %s %s", i.GoVersion, i.Platform)
}
