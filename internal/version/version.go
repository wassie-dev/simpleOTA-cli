// Package version holds build metadata.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is set at link time by goreleaser with
// -ldflags "-X .../internal/version.Version=1.2.3".
var Version = "dev"

// Current is the release version, or for a go install build the module
// version Go recorded in the binary, without its leading "v" so both read
// the same.
func Current() string {
	bi, _ := debug.ReadBuildInfo()
	return current(Version, bi)
}

func current(linked string, bi *debug.BuildInfo) string {
	if linked != "dev" {
		return linked
	}
	if bi != nil && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return linked
}

// UserAgent identifies the CLI to the SimpleOTA API.
func UserAgent() string {
	return "simpleota-cli/" + Current()
}
