// Package version holds build metadata set at link time by goreleaser.
package version

// Version is overridden with -ldflags "-X .../internal/version.Version=v1.2.3".
var Version = "dev"

// UserAgent identifies the CLI to the SimpleOTA API.
func UserAgent() string {
	return "simpleota-cli/" + Version
}
