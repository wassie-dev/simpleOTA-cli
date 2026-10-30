package api

import (
	"regexp"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a UUID, the form of every SimpleOTA object ID.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// Project mirrors the server's ProjectOut. A project token's /me/ answer
// carries only the ID and name.
type Project struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Slug             string    `json:"slug"`
	Description      string    `json:"description"`
	DefaultFramework string    `json:"default_framework"`
	BuildCounter     int       `json:"build_counter"`
	CreatedAt        time.Time `json:"created_at"`
}

// Me mirrors PersonalMeOut and ProjectMeOut: Account is set for a personal
// token, Project for a project token.
type Me struct {
	TokenType string   `json:"token_type"`
	Account   *Account `json:"account"`
	Project   *Project `json:"project"`
	Scopes    []string `json:"scopes"`
}

// Account mirrors UserIdentityOut.
type Account struct {
	ID       int    `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	IsStaff  bool   `json:"is_staff"`
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

// Artifact mirrors ArtifactOut, the server's name for a build.
type Artifact struct {
	ID               string    `json:"id"`
	BuildNumber      int       `json:"build_number"`
	VersionLabel     string    `json:"version_label"`
	Framework        string    `json:"framework"`
	ChipFamily       string    `json:"chip_family"`
	BoardID          string    `json:"board_id"`
	PartitionProfile string    `json:"partition_profile"`
	NVSSchemaVersion *int      `json:"nvs_schema_version"`
	ChecksumSHA256   string    `json:"checksum_sha256"`
	FileSize         int64     `json:"file_size"`
	SecurityMode     string    `json:"security_mode"`
	Archived         bool      `json:"archived"`
	CreatedAt        time.Time `json:"created_at"`
	Warnings         []string  `json:"warnings"`
}

// Device mirrors DeviceOut.
type Device struct {
	ID                  string     `json:"id"`
	DeviceID            string     `json:"device_id"`
	Name                string     `json:"name"`
	Framework           string     `json:"framework"`
	ChipFamily          string     `json:"chip_family"`
	BoardID             string     `json:"board_id"`
	HardwareRevision    string     `json:"hardware_revision"`
	PartitionProfile    string     `json:"partition_profile"`
	NVSSchemaVersion    *int       `json:"nvs_schema_version"`
	SecurityMode        string     `json:"security_mode"`
	CurrentBuildNumber  *int       `json:"current_build_number"`
	CurrentVersionLabel string     `json:"current_version_label"`
	LastOTAStatus       string     `json:"last_ota_status"`
	Active              bool       `json:"active"`
	LastSeen            *time.Time `json:"last_seen"`
	LastCheckAt         *time.Time `json:"last_check_at"`
	LastCheckStatus     string     `json:"last_check_status"`
	CheckState          string     `json:"check_state"`
	ChannelID           *string    `json:"channel_id"`
	GroupIDs            []string   `json:"group_ids"`
}

// DeviceFilter narrows a device listing; empty fields do not filter.
type DeviceFilter struct {
	ChannelID string
	GroupID   string
	Active    *bool
}

// Channel mirrors ChannelOut.
type Channel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
}

// Group mirrors DeviceGroupOut.
type Group struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
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
