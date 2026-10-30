package deploy

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// OverlapAPI is the part of the developer API AlsoActive needs.
type OverlapAPI interface {
	ListDeployments(ctx context.Context, projectID string) ([]api.Deployment, error)
	ListArtifacts(ctx context.Context, projectID string) ([]api.Artifact, error)
}

// AlsoActive returns the project's other active deployments that reach the
// given deployment's channels with a build for the same chip. A device
// installs the highest build offered to it, so these go on reaching the
// devices the given deployment does not.
func AlsoActive(ctx context.Context, a OverlapAPI, projectID, deploymentID, chip string) ([]api.Deployment, error) {
	deps, err := a.ListDeployments(ctx, projectID)
	if err != nil {
		return nil, err
	}
	artifacts, err := a.ListArtifacts(ctx, projectID)
	if err != nil {
		return nil, err
	}
	chips := make(map[string]string, len(artifacts))
	for _, art := range artifacts {
		chips[art.ID] = art.ChipFamily
	}
	var channels []string
	for _, d := range deps {
		if d.ID == deploymentID {
			channels = d.TargetChannelIDs
		}
	}
	var out []api.Deployment
	for _, d := range deps {
		if d.ID == deploymentID || d.State != api.StateActive || !overlaps(channels, d.TargetChannelIDs) {
			continue
		}
		if c, ok := chips[d.ArtifactID]; ok && c != "" && !strings.EqualFold(c, chip) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ArtifactBuildNumber > out[j].ArtifactBuildNumber })
	return out, nil
}

// overlaps reports whether two deployments' channel targets share a channel.
// A deployment without channel targets reaches every channel.
func overlaps(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
