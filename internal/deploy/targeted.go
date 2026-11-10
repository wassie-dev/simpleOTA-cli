package deploy

import (
	"context"
	"slices"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// DevicesAPI is the part of the developer API Targeted needs.
type DevicesAPI interface {
	ListDevices(ctx context.Context, projectID string, f api.DeviceFilter) ([]api.Device, error)
}

// Targeted counts the project's active devices in a deployment's audience,
// by the server's rule: a device must be on one of the targeted channels, if
// any are targeted, and in one of the targeted groups, if any are targeted.
// The rollout percentage and the build's compatibility checks then decide
// which of these devices are offered the build; the developer API exposes
// neither the rollout seed nor every compatibility field, so they are not
// applied here.
func Targeted(ctx context.Context, a DevicesAPI, projectID string, d api.Deployment) (int, error) {
	active := true
	devices, err := a.ListDevices(ctx, projectID, api.DeviceFilter{Active: &active})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, dev := range devices {
		if inAudience(dev, d) {
			n++
		}
	}
	return n, nil
}

func inAudience(dev api.Device, d api.Deployment) bool {
	if len(d.TargetChannelIDs) > 0 && (dev.ChannelID == nil || !slices.Contains(d.TargetChannelIDs, *dev.ChannelID)) {
		return false
	}
	if len(d.TargetGroupIDs) > 0 && !slices.ContainsFunc(dev.GroupIDs, func(g string) bool { return slices.Contains(d.TargetGroupIDs, g) }) {
		return false
	}
	return true
}
