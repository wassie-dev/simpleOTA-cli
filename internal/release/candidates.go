package release

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

// request is what an upload asked the server to create.
type request struct {
	sha, label, framework, chip, security string
	// target is set for a release, which also deploys.
	target *Target
}

// uncertainErr explains an upload that failed without showing whether the
// server created anything. It reads the project and lists each build of the
// binary, with its deployments for a release, and how each differs from the
// request. It claims none of them: another upload of the same binary, or a
// release the server did not finish, looks the same. Nothing is sent again.
func (s Service) uncertainErr(ctx context.Context, err error, req request) error {
	found, lookErr := s.candidates(ctx, req)
	switch {
	case lookErr != nil:
		return fmt.Errorf("%w\nThe upload may still have created a build, and checking the project failed:\n%v\nRun the same command again to find out: it stops if the project already has\nthis binary.", err, lookErr)
	case found == "":
		return fmt.Errorf("%w\nThe upload may still have created a build, though the project shows none of\nthis binary yet. Run the same command again to find out: it stops if the\nproject already has this binary.", err)
	}
	return fmt.Errorf("%w\nIt is not known whether the upload created anything. The project has these\nbuilds of this binary. Each may be from this upload, from another upload of\nthe same binary, or from a release the server did not finish:%s\nCheck them before acting. Nothing was uploaded again.", err, found)
}

// candidates describes the project's builds of the binary, and for a
// release their deployments, or returns "" when there are none.
func (s Service) candidates(ctx context.Context, req request) (string, error) {
	artifacts, err := s.API.ListArtifacts(ctx, s.ProjectID)
	if err != nil {
		return "", err
	}
	artifacts = slices.DeleteFunc(artifacts, func(a api.Artifact) bool { return !strings.EqualFold(a.ChecksumSHA256, req.sha) })
	if len(artifacts) == 0 {
		return "", nil
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].BuildNumber < artifacts[j].BuildNumber })
	var channels map[string]string
	var deps []api.Deployment
	if req.target != nil {
		if channels, err = s.channelNames(ctx); err != nil {
			return "", err
		}
		if deps, err = s.API.ListDeployments(ctx, s.ProjectID); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	for _, a := range artifacts {
		fmt.Fprintf(&b, "\n  build %d, version %q: %s", a.BuildNumber, a.VersionLabel, verdict(buildDiffs(a, req)))
		if req.target == nil {
			continue
		}
		n := 0
		for _, d := range deps {
			if d.ArtifactID != a.ID {
				continue
			}
			n++
			fmt.Fprintf(&b, "\n    deployment %s, %s at %d%% to %s: %s", termtext.Clean(d.ID), termtext.Clean(d.State), d.RolloutPercentage,
				targetText(d, channels), verdict(deploymentDiffs(d, channels, *req.target)))
		}
		if n == 0 {
			b.WriteString("\n    no deployment")
		}
	}
	if req.security == "signed" {
		b.WriteString("\n  The server does not show which key signed a build, so the key is not compared.")
	}
	return b.String(), nil
}

func (s Service) channelNames(ctx context.Context) (map[string]string, error) {
	channels, err := s.API.ListChannels(ctx, s.ProjectID)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(channels))
	for _, c := range channels {
		names[c.ID] = c.Name
	}
	return names, nil
}

func buildDiffs(a api.Artifact, req request) []string {
	var d []string
	if a.VersionLabel != req.label {
		d = append(d, fmt.Sprintf("version %q, not %q", a.VersionLabel, req.label))
	}
	if a.SecurityMode != req.security {
		d = append(d, fmt.Sprintf("security %q, not %q", a.SecurityMode, req.security))
	}
	if a.ChipFamily != req.chip {
		d = append(d, fmt.Sprintf("chip %q, not %q", a.ChipFamily, req.chip))
	}
	if a.Framework != req.framework {
		d = append(d, fmt.Sprintf("framework %q, not %q", a.Framework, req.framework))
	}
	if a.Archived {
		d = append(d, "archived")
	}
	return d
}

func deploymentDiffs(dep api.Deployment, channels map[string]string, t Target) []string {
	var d []string
	if dep.State != api.StateActive {
		d = append(d, fmt.Sprintf("%s, not active", termtext.Clean(dep.State)))
	}
	if dep.RolloutPercentage != t.Percent {
		d = append(d, fmt.Sprintf("%d%%, not %d%%", dep.RolloutPercentage, t.Percent))
	}
	if len(dep.TargetGroupIDs) > 0 || len(dep.TargetChannelIDs) != 1 || channels[dep.TargetChannelIDs[0]] != t.Channel {
		d = append(d, fmt.Sprintf("not only channel %q", t.Channel))
	}
	return d
}

func targetText(d api.Deployment, channels map[string]string) string {
	var parts []string
	for _, id := range d.TargetChannelIDs {
		name, ok := channels[id]
		if !ok {
			name = id
		}
		parts = append(parts, fmt.Sprintf("channel %q", name))
	}
	if n := len(d.TargetGroupIDs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d groups", n))
	}
	if len(parts) == 0 {
		return "every device"
	}
	return strings.Join(parts, " and ")
}

func verdict(diffs []string) string {
	if len(diffs) == 0 {
		return "matches the request"
	}
	return "differs: " + strings.Join(diffs, "; ")
}
