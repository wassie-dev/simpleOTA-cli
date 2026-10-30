package api

import (
	"regexp"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a UUID, the form of every SimpleOTA object ID.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// Project mirrors the server's ProjectOut.
type Project struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	DefaultFramework string `json:"default_framework"`
}

// SigningKey mirrors SigningKeyOut.
type SigningKey struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	KeyID        string     `json:"key_id"`
	Algorithm    string     `json:"algorithm"`
	PublicKeyPEM string     `json:"public_key_pem"`
	RevokedAt    *time.Time `json:"revoked_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Artifact mirrors the fields of ArtifactOut the CLI uses.
type Artifact struct {
	ID             string `json:"id"`
	BuildNumber    int    `json:"build_number"`
	VersionLabel   string `json:"version_label"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	FileSize       int64  `json:"file_size"`
	SecurityMode   string `json:"security_mode"`
	ChipFamily     string `json:"chip_family"`
}

// Device mirrors the fields of DeviceOut the CLI uses.
type Device struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	ChipFamily string `json:"chip_family"`
}

// Deployment mirrors DeploymentOut.
type Deployment struct {
	ID                  string     `json:"id"`
	ProjectID           string     `json:"project_id"`
	ArtifactID          string     `json:"artifact_id"`
	ArtifactBuildNumber int        `json:"artifact_build_number"`
	RolloutStrategy     string     `json:"rollout_strategy"`
	RolloutPercentage   int        `json:"rollout_percentage"`
	TargetChannelIDs    []string   `json:"target_channel_ids"`
	State               string     `json:"state"`
	StartedAt           *time.Time `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	CreatedAt           time.Time  `json:"created_at"`
}

// Deployment states.
const (
	StateDraft     = "draft"
	StateActive    = "active"
	StatePaused    = "paused"
	StateCompleted = "completed"
	StateCancelled = "cancelled"
)

// DeviceState mirrors DeploymentDeviceStateOut.
type DeviceState struct {
	ID                string `json:"id"`
	DeviceID          string `json:"device_id"`
	FinalStatus       string `json:"final_status"`
	Eligible          bool   `json:"eligible"`
	AssignedToRollout bool   `json:"assigned_to_rollout"`
	StatusReason      string `json:"status_reason"`
}

// Device lifecycle statuses reported through the OTA status endpoint.
const (
	DevicePending    = "pending"
	DeviceConfirmed  = "confirmed"
	DeviceFailed     = "failed"
	DeviceRolledBack = "rolled_back"
)

// ReleaseRequest is one call to the quick-release endpoint.
type ReleaseRequest struct {
	ManifestJSON      []byte
	FileName          string
	Binary            []byte
	RolloutPercentage int
	ChannelName       string
}

// ReleaseResult mirrors ReleaseOut.
type ReleaseResult struct {
	ArtifactID   string `json:"artifact_id"`
	BuildNumber  int    `json:"build_number"`
	DeploymentID string `json:"deployment_id"`
	State        string `json:"state"`
	Channel      string `json:"channel"`
}

// Transition is a deployment state change. RolloutPercentage is sent only
// for ramp.
type Transition struct {
	Action            string `json:"action"`
	RolloutPercentage *int   `json:"rollout_percentage,omitempty"`
}
