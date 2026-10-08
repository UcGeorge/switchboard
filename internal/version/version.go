// Package version holds build metadata injected via -ldflags.
package version

import (
	"fmt"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version line.
func String() string {
	if Commit == "none" {
		return Effective()
	}
	return fmt.Sprintf("%s (%s, %s)", Effective(), Commit, Date)
}

// Effective includes module version metadata for `go install ...@version` builds.
func Effective() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}
