package output

import (
	"bytes"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
)

var (
	created = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	started = created.Add(time.Second)
	dep     = api.Deployment{
		ID: "5f0c2b1e-1d2c-4b7a-9a3e-0123456789ab", ProjectID: "11111111-1111-4111-8111-111111111111",
		ArtifactID: "a", ArtifactBuildNumber: 42, RolloutStrategy: "immediate", RolloutPercentage: 25,
		State: api.StatePaused, CreatedAt: created, StartedAt: &started,
	}
)

func TestRolloutGolden(t *testing.T) {
	v := RolloutView{Action: "pause", Deployment: NewDeploymentView(dep), NextStep: NextStep(dep)}
	golden(t, "rollout.json", render(t, true, func(p Printer) error { return p.Rollout(v) }))
	golden(t, "rollout.txt", render(t, false, func(p Printer) error { return p.Rollout(v) }))
}

func TestStatusGolden(t *testing.T) {
	s := deploy.Summarize(dep, []api.DeviceState{
		{DeviceID: "d1", FinalStatus: "confirmed", AssignedToRollout: true},
		{DeviceID: "d2", FinalStatus: "downloaded", AssignedToRollout: true},
		{DeviceID: "d3", FinalStatus: "rolled_back", AssignedToRollout: true, StatusReason: "task_wdt"},
	})
	v := NewStatusView(s, deploy.Evaluate(s))
	golden(t, "status.json", render(t, true, func(p Printer) error { return p.Status(v) }))
	golden(t, "status.txt", render(t, false, func(p Printer) error { return p.Status(v) }))
}

func TestStatusEmptyFailuresIsArray(t *testing.T) {
	active := dep
	active.State = api.StateActive
	v := NewStatusView(deploy.Summarize(active, nil), deploy.OutcomeInProgress)
	golden(t, "status_empty.json", render(t, true, func(p Printer) error { return p.Status(v) }))
}

func TestNoColorWhenDisabled(t *testing.T) {
	var out bytes.Buffer
	p := Printer{Out: &out, Err: &out}
	_ = p.Rollout(RolloutView{Deployment: NewDeploymentView(dep)})
	if bytes.Contains(out.Bytes(), []byte("\x1b[")) {
		t.Fatal("colour codes printed with Color=false")
	}
	out.Reset()
	p.Color = true
	_ = p.Rollout(RolloutView{Deployment: NewDeploymentView(dep)})
	if !bytes.Contains(out.Bytes(), []byte("\x1b[")) {
		t.Fatal("expected colour codes with Color=true")
	}
}
