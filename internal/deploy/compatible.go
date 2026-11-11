package deploy

import (
	"context"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// devicesPageSize is the most devices the server returns in one page.
const devicesPageSize = 500

// CompatibleDevices fetches every page of the devices the server counts in a
// deployment's compatible_audience_device_count: active devices in its
// audience whose stored metadata is compatible with its build, ordered by
// device_id. The rollout percentage is not applied.
//
// Each page is a separate request, so a device that checks in between them
// can shift later pages by one; the latest count decides when to stop.
func CompatibleDevices(ctx context.Context, a API, projectID, deploymentID string) ([]api.Device, error) {
	devices := []api.Device{}
	for offset := 0; ; offset += devicesPageSize {
		page, err := a.ListDeploymentDevices(ctx, projectID, deploymentID, devicesPageSize, offset)
		if err != nil {
			return nil, err
		}
		devices = append(devices, page.Items...)
		// The server leaves out a device deleted while it built the page, so
		// a short page does not mean the last one; the count does.
		if offset+devicesPageSize >= page.Count {
			return devices, nil
		}
	}
}
