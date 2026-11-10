package output

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/release"
)

func TestReleaseGolden(t *testing.T) {
	r := NewReleaseView(release.Result{
		BuildNumber: 42, ArtifactID: "art", DeploymentID: "5f0c2b1e-1d2c-4b7a-9a3e-0123456789ab", State: "active", Channel: "stable",
		SecurityMode: "signed", KeyID: "prod-2026", Attested: true, Chip: "esp32s3", SHA256: "ab12", Size: 1024,
	})
	golden(t, "release.json", render(t, true, func(p Printer) error { return p.Release(r) }))
	golden(t, "release.txt", render(t, false, func(p Printer) error { return p.Release(r) }))

	r.AlsoActive = []DeploymentView{{ID: "0d8e7a14-3b5c-4f2a-8e1d-abcdef012345", BuildNumber: 41, State: "active", RolloutStrategy: "immediate", RolloutPercentage: 100}}
	golden(t, "release_also_active.json", render(t, true, func(p Printer) error { return p.Release(r) }))
	golden(t, "release_also_active.txt", render(t, false, func(p Printer) error { return p.Release(r) }))
}

func TestReleaseTextShowsServerIDsEscaped(t *testing.T) {
	var out bytes.Buffer
	p := Printer{Out: &out, Err: io.Discard}
	if err := p.Release(ReleaseView{DeploymentID: "d\x1b[2J\nError: forged", State: "active", Channel: "stable"}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b") || strings.Contains(out.String(), "\nError: forged") {
		t.Fatalf("%q", out.String())
	}
}
