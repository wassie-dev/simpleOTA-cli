package deploy

import (
	"context"
	"fmt"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// devicesPageSize is the most devices the server returns in one page.
const devicesPageSize = 500

// CompatibleDevices fetches the devices the server counts in a deployment's
// compatible_audience_device_count: active devices in its audience whose
// stored metadata is compatible with its build, ordered by device_id. The
// rollout percentage is not applied. It also returns the count from the
// first page.
//
// With max above 0 it stops once it has max devices; otherwise it reads
// every page. Each page is a separate request, so a device that changes
// between them can shift later pages and be missed. A device served twice
// is listed once.
func CompatibleDevices(ctx context.Context, a API, projectID, deploymentID string, max int) ([]api.Device, int, error) {
	devices := []api.Device{}
	seen := map[string]bool{}
	total := -1
	// offset steps by the devices actually served, so a server that serves
	// fewer than asked for skips none.
	for offset := 0; ; {
		limit := devicesPageSize
		if max > 0 {
			limit = min(limit, max-len(devices))
		}
		page, err := a.ListDeploymentDevices(ctx, projectID, deploymentID, limit, offset)
		if err != nil {
			return nil, 0, err
		}
		if total < 0 {
			total = page.Count
		}
		for _, d := range page.Items {
			if !seen[d.ID] {
				seen[d.ID] = true
				devices = append(devices, d)
			}
		}
		offset += len(page.Items)
		if offset >= page.Count || (max > 0 && len(devices) >= max) {
			return devices, total, nil
		}
		if len(page.Items) == 0 {
			return nil, 0, fmt.Errorf("the server returned no devices at offset %d of %d", offset, page.Count)
		}
	}
}
