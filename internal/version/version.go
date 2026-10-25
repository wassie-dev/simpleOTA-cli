// Package version holds build metadata.
package version

import "runtime/debug"

// Version is set at link time by goreleaser with
// -ldflags "-X .../internal/version.Version=1.2.3".
var Version = "dev"

// Current is the release version, or for a go install build the module
// version Go recorded in the binary.
func Current() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

// UserAgent identifies the CLI to the SimpleOTA API.
func UserAgent() string {
	return "simpleota-cli/" + Current()
}
