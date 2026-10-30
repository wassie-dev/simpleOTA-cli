package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

func TestAlsoActive(t *testing.T) {
	fake := fakeapi.New(t)
	fake.Artifacts = []api.Artifact{
		{ID: "a1", BuildNumber: 1, ChipFamily: "esp32s3"},
		{ID: "a2", BuildNumber: 2, ChipFamily: "esp32"},
		{ID: "a3", BuildNumber: 3, ChipFamily: "esp32s3"},
	}
	stable := []string{fakeapi.StableChannelID}
	fake.Deployments = []api.Deployment{
		{ID: "new", ArtifactID: "a3", ArtifactBuildNumber: 3, State: api.StateActive, TargetChannelIDs: stable},
		{ID: "same-chip", ArtifactID: "a1", ArtifactBuildNumber: 1, State: api.StateActive, TargetChannelIDs: stable},
		{ID: "other-chip", ArtifactID: "a2", ArtifactBuildNumber: 2, State: api.StateActive, TargetChannelIDs: stable},
		{ID: "all-channels", ArtifactID: "unknown", State: api.StateActive},
		{ID: "paused", ArtifactID: "a1", ArtifactBuildNumber: 1, State: api.StatePaused, TargetChannelIDs: stable},
		{ID: "other-channel", ArtifactID: "a1", ArtifactBuildNumber: 1, State: api.StateActive, TargetChannelIDs: []string{"beta"}},
	}
	got, err := AlsoActive(context.Background(), api.New(fake.URL, fakeapi.ProjectToken, "test"), fakeapi.ProjectID, "new", "esp32s3")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(got))
	for i, d := range got {
		ids[i] = d.ID
	}
	if strings.Join(ids, ",") != "same-chip,all-channels" {
		t.Fatalf("also active = %v", ids)
	}
}
