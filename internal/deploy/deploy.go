// Package deploy changes deployment state, lists the devices whose metadata
// is compatible with a deployment, and summarises per-device rollout
// progress.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// API is the part of the developer API that deployments need.
type API interface {
	ListDeployments(ctx context.Context, projectID string) ([]api.Deployment, error)
	GetDeployment(ctx context.Context, projectID, deploymentID string) (api.Deployment, error)
	ListDeploymentStates(ctx context.Context, projectID, deploymentID string) ([]api.DeviceState, error)
	ListDevices(ctx context.Context, projectID string, f api.DeviceFilter) ([]api.Device, error)
	ListDeploymentDevices(ctx context.Context, projectID, deploymentID string, limit, offset int) (api.DeploymentDevicesPage, error)
}

// ActionStart starts a draft deployment. deployment create sends it; it is
// not a command of its own.
const ActionStart = "start"

// Actions accepted by the transition endpoint that the CLI exposes.
const (
	ActionRamp     = "ramp"
	ActionPause    = "pause"
	ActionResume   = "resume"
	ActionComplete = "complete"
	ActionCancel   = "cancel"
)

// Actions lists the rollout actions in help order.
var Actions = []string{ActionRamp, ActionPause, ActionResume, ActionComplete, ActionCancel}

// BuildTransition validates an action and its percentage.
func BuildTransition(action string, percent *int) (api.Transition, error) {
	t := api.Transition{Action: action}
	switch action {
	case ActionRamp:
		if percent == nil {
			return t, errors.New("ramp needs --percent")
		}
		if *percent < 0 || *percent > 100 {
			return t, errors.New("--percent must be between 0 and 100")
		}
		t.RolloutPercentage = percent
	case ActionPause, ActionResume, ActionComplete, ActionCancel:
		if percent != nil {
			return t, fmt.Errorf("--percent applies only to ramp, not %s", action)
		}
	default:
		return t, fmt.Errorf("unknown rollout action %q: use ramp, pause, resume, complete or cancel", action)
	}
	return t, nil
}

// TargetsAllDevices reports whether a deployment targets every device in
// the project, having no channel or group targets.
func TargetsAllDevices(d api.Deployment) bool {
	return len(d.TargetChannelIDs) == 0 && len(d.TargetGroupIDs) == 0
}

// Latest returns the project's most recently created deployment.
func Latest(ctx context.Context, a API, projectID string) (api.Deployment, error) {
	deps, err := a.ListDeployments(ctx, projectID)
	if err != nil {
		return api.Deployment{}, err
	}
	if len(deps) == 0 {
		return api.Deployment{}, errors.New("this project has no deployments yet\nCreate one with:\n  simpleota release <bin> --version-label <label>")
	}
	latest := deps[0]
	for _, d := range deps[1:] {
		if d.CreatedAt.After(latest.CreatedAt) {
			latest = d
		}
	}
	return latest, nil
}

// Counts tallies devices by where they are in the deployment.
type Counts struct {
	Offered    int `json:"offered"`
	InProgress int `json:"in_progress"`
	Confirmed  int `json:"confirmed"`
	Failed     int `json:"failed"`
	RolledBack int `json:"rolled_back"`
}

// Failure is one device that failed or rolled back.
type Failure struct {
	// DeviceID is the device's SimpleOTA UUID.
	DeviceID string `json:"device_id"`
	// Device is the ID the device reports, when it could be looked up.
	Device string `json:"device,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Summary is a deployment with its device progress.
type Summary struct {
	Deployment api.Deployment
	Counts     Counts
	Failures   []Failure
}

// Summarize tallies device states. A state row exists only once the server
// has offered the build to a device or the device has reported against the
// deployment, so Offered counts the devices the current step has reached.
func Summarize(d api.Deployment, states []api.DeviceState) Summary {
	s := Summary{Deployment: d}
	for _, st := range states {
		if st.FinalStatus == api.DevicePending && !st.AssignedToRollout {
			continue
		}
		s.Counts.Offered++
		switch st.FinalStatus {
		case api.DeviceConfirmed:
			s.Counts.Confirmed++
		case api.DeviceFailed:
			s.Counts.Failed++
			s.Failures = append(s.Failures, Failure{DeviceID: st.DeviceID, Status: st.FinalStatus, Reason: st.StatusReason})
		case api.DeviceRolledBack:
			s.Counts.RolledBack++
			s.Failures = append(s.Failures, Failure{DeviceID: st.DeviceID, Status: st.FinalStatus, Reason: st.StatusReason})
		default:
			s.Counts.InProgress++
		}
	}
	return s
}

// Outcome is the verdict for a summary.
type Outcome string

const (
	OutcomeInProgress   Outcome = "in_progress"
	OutcomeSucceeded    Outcome = "succeeded"
	OutcomeDeviceFailed Outcome = "device_failed"
	OutcomeStopped      Outcome = "stopped"
	OutcomeNoDevices    Outcome = "no_devices"
)

// Evaluate gives a summary's verdict: success once every device the current
// step reached has confirmed; failure on any failed or rolled-back device,
// when the deployment is no longer running, or when it was completed before
// any device was offered the build.
func Evaluate(s Summary) Outcome {
	if s.Counts.Failed > 0 || s.Counts.RolledBack > 0 {
		return OutcomeDeviceFailed
	}
	switch s.Deployment.State {
	case api.StatePaused, api.StateCancelled, api.StateDraft:
		return OutcomeStopped
	case api.StateCompleted:
		if s.Counts.Offered == 0 {
			return OutcomeNoDevices
		}
		return OutcomeSucceeded
	}
	if s.Counts.Offered > 0 && s.Counts.Confirmed == s.Counts.Offered {
		return OutcomeSucceeded
	}
	return OutcomeInProgress
}

// Status fetches a deployment and its device states.
func Status(ctx context.Context, a API, projectID, deploymentID string) (Summary, error) {
	d, err := a.GetDeployment(ctx, projectID, deploymentID)
	if err != nil {
		return Summary{}, err
	}
	states, err := a.ListDeploymentStates(ctx, projectID, deploymentID)
	if err != nil {
		return Summary{}, err
	}
	s := Summarize(d, states)
	nameFailures(ctx, a, projectID, s.Failures)
	return s, nil
}

// nameFailures fills in the ID each failed device reports. A failed lookup
// leaves the UUIDs alone.
func nameFailures(ctx context.Context, a API, projectID string, failures []Failure) {
	if len(failures) == 0 {
		return
	}
	devices, err := a.ListDevices(ctx, projectID, api.DeviceFilter{})
	if err != nil {
		return
	}
	reported := make(map[string]string, len(devices))
	for _, d := range devices {
		reported[d.ID] = d.DeviceID
	}
	for i := range failures {
		failures[i].Device = reported[failures[i].DeviceID]
	}
}

// Name is how output names the device: its reported ID when that is plain
// printable text, or else its UUID. A device chooses its own ID, so an ID
// with control or formatting characters is not printed.
func (f Failure) Name() string {
	if f.Device != "" && !strings.ContainsFunc(f.Device, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return f.Device
	}
	return f.DeviceID
}
