// Package deploy changes deployment state and summarises and waits on
// per-device rollout progress.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

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

// Latest returns the project's most recently created deployment.
func Latest(ctx context.Context, a api.API, projectID string) (api.Deployment, error) {
	deps, err := a.ListDeployments(ctx, projectID)
	if err != nil {
		return api.Deployment{}, err
	}
	if len(deps) == 0 {
		return api.Deployment{}, errors.New("this project has no deployments yet; create one with simpleota release")
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
	DeviceID string `json:"device_id"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
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

// Outcome is the wait verdict for a summary.
type Outcome string

const (
	OutcomeInProgress   Outcome = "in_progress"
	OutcomeSucceeded    Outcome = "succeeded"
	OutcomeDeviceFailed Outcome = "device_failed"
	OutcomeStopped      Outcome = "stopped"
	OutcomeNoDevices    Outcome = "no_devices"
	OutcomeTimedOut     Outcome = "timed_out"
)

// Evaluate applies the deployment wait rule: success once every device the current
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
func Status(ctx context.Context, a api.API, projectID, deploymentID string) (Summary, error) {
	d, err := a.GetDeployment(ctx, projectID, deploymentID)
	if err != nil {
		return Summary{}, err
	}
	states, err := a.ListDeploymentStates(ctx, projectID, deploymentID)
	if err != nil {
		return Summary{}, err
	}
	return Summarize(d, states), nil
}

// Waiter polls a deployment until Evaluate reaches a verdict or the timeout
// passes.
type Waiter struct {
	API        api.API
	ProjectID  string
	Interval   time.Duration
	Timeout    time.Duration
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
	OnProgress func(Summary)
	// OnPollError is called when a poll fails transiently and waiting goes on.
	OnPollError func(error)
}

// Wait returns the last summary and its outcome. The outcome is
// OutcomeTimedOut when the timeout passes first. A transient poll failure
// (network, 429, 5xx gateway errors) does not end the wait; any other error
// does.
func (w Waiter) Wait(ctx context.Context, deploymentID string) (Summary, Outcome, error) {
	deadline := w.Now().Add(w.Timeout)
	var last Summary
	var lastCounts *Counts
	var lastErr error
	for {
		s, err := Status(ctx, w.API, w.ProjectID, deploymentID)
		switch {
		case err == nil:
			last, lastErr = s, nil
			if w.OnProgress != nil && (lastCounts == nil || *lastCounts != s.Counts) {
				c := s.Counts
				lastCounts = &c
				w.OnProgress(s)
			}
			if o := Evaluate(s); o != OutcomeInProgress {
				return s, o, nil
			}
		case api.IsTransient(err) && ctx.Err() == nil:
			lastErr = err
			if w.OnPollError != nil {
				w.OnPollError(err)
			}
		default:
			return last, OutcomeInProgress, err
		}
		remaining := deadline.Sub(w.Now())
		if remaining <= 0 {
			if lastCounts == nil && lastErr != nil {
				return last, OutcomeInProgress, lastErr
			}
			return last, OutcomeTimedOut, nil
		}
		if err := w.Sleep(ctx, min(w.Interval, remaining)); err != nil {
			return last, OutcomeInProgress, err
		}
	}
}
