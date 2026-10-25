package output

import (
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/release"
)

func TestReleaseGolden(t *testing.T) {
	r := NewReleaseView(release.Result{
		BuildNumber: 42, ArtifactID: "art", DeploymentID: "5f0c2b1e-1d2c-4b7a-9a3e-0123456789ab", State: "active", Channel: "stable",
		SecurityMode: "signed", KeyID: "prod-2026", SHA256: "ab12", SHA256Verified: true, Size: 1024,
	})
	golden(t, "release.json", render(t, true, func(p Printer) error { return p.Release(r) }))
	golden(t, "release.txt", render(t, false, func(p Printer) error { return p.Release(r) }))
}
