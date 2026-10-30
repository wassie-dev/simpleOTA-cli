package output

import (
	"fmt"
	"strconv"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// DeviceView is a device, with its channel's name resolved.
type DeviceView struct {
	ID                  string     `json:"id"`
	DeviceID            string     `json:"device_id"`
	Name                string     `json:"name"`
	Framework           string     `json:"framework"`
	Chip                string     `json:"chip"`
	BoardID             string     `json:"board_id"`
	HardwareRevision    string     `json:"hardware_revision"`
	PartitionProfile    string     `json:"partition_profile"`
	NVSSchemaVersion    *int       `json:"nvs_schema_version"`
	SecurityMode        string     `json:"security_mode"`
	CurrentBuildNumber  *int       `json:"current_build_number"`
	CurrentVersionLabel string     `json:"current_version_label"`
	LastOTAStatus       string     `json:"last_ota_status"`
	Active              bool       `json:"active"`
	CheckState          string     `json:"check_state"`
	LastCheckStatus     string     `json:"last_check_status"`
	LastCheckAt         *time.Time `json:"last_check_at"`
	LastSeen            *time.Time `json:"last_seen"`
	ChannelID           *string    `json:"channel_id"`
	Channel             string     `json:"channel"`
	GroupIDs            []string   `json:"group_ids"`
}

// NewDeviceView converts an API device; channels maps channel IDs to names.
func NewDeviceView(d api.Device, channels map[string]string) DeviceView {
	v := DeviceView{
		ID: d.ID, DeviceID: d.DeviceID, Name: d.Name, Framework: d.Framework, Chip: d.ChipFamily, BoardID: d.BoardID,
		HardwareRevision: d.HardwareRevision, PartitionProfile: d.PartitionProfile, NVSSchemaVersion: d.NVSSchemaVersion,
		SecurityMode: d.SecurityMode, CurrentBuildNumber: d.CurrentBuildNumber, CurrentVersionLabel: d.CurrentVersionLabel,
		LastOTAStatus: d.LastOTAStatus, Active: d.Active, CheckState: d.CheckState, LastCheckStatus: d.LastCheckStatus,
		LastCheckAt: d.LastCheckAt, LastSeen: d.LastSeen, ChannelID: d.ChannelID, GroupIDs: d.GroupIDs,
	}
	if v.GroupIDs == nil {
		v.GroupIDs = []string{}
	}
	if d.ChannelID != nil {
		v.Channel = channels[*d.ChannelID]
	}
	return v
}

func (d DeviceView) build() string {
	if d.CurrentBuildNumber == nil {
		return ""
	}
	s := strconv.Itoa(*d.CurrentBuildNumber)
	if d.CurrentVersionLabel != "" {
		s += " (" + d.CurrentVersionLabel + ")"
	}
	return s
}

func (d DeviceView) state() string {
	if !d.Active {
		return "inactive"
	}
	return d.CheckState
}

// DeviceList prints device list.
func (p Printer) DeviceList(devices []DeviceView) error {
	if p.JSON {
		return p.WriteJSON(devices)
	}
	if len(devices) == 0 {
		fmt.Fprintln(p.Out, "No devices match. Devices appear here once their firmware checks in.")
		return nil
	}
	rows := make([][]string, len(devices))
	for i, d := range devices {
		rows[i] = []string{d.DeviceID, d.Name, d.Chip, d.build(), d.Channel, d.state(), whenPtr(d.LastSeen)}
	}
	return p.table([]string{"DEVICE", "NAME", "CHIP", "BUILD", "CHANNEL", "STATE", "LAST SEEN"}, rows)
}

// DeviceShow prints device show.
func (p Printer) DeviceShow(d DeviceView) error {
	if p.JSON {
		return p.WriteJSON(d)
	}
	fmt.Fprintf(p.Out, "Device %s\n", p.bold(d.DeviceID))
	p.field("name", d.Name)
	p.field("id", d.ID)
	p.field("build", d.build())
	p.field("channel", d.Channel)
	p.field("state", d.state())
	p.field("last check", d.LastCheckStatus)
	p.field("checked at", whenPtr(d.LastCheckAt))
	p.field("last seen", whenPtr(d.LastSeen))
	p.field("last ota", d.LastOTAStatus)
	p.field("framework", d.Framework)
	p.field("chip", d.Chip)
	p.field("board", d.BoardID)
	p.field("hardware", d.HardwareRevision)
	p.field("partitions", d.PartitionProfile)
	if d.NVSSchemaVersion != nil {
		p.field("nvs schema", strconv.Itoa(*d.NVSSchemaVersion))
	}
	p.field("security", d.SecurityMode)
	return nil
}

// CheckEventView is one OTA check by a device.
type CheckEventView struct {
	At                  time.Time `json:"at"`
	Decision            string    `json:"decision"`
	Status              string    `json:"status"`
	DeploymentID        *string   `json:"deployment_id"`
	OfferedBuildNumber  *int      `json:"offered_build_number"`
	ReportedBuildNumber *int      `json:"reported_build_number"`
	ReportedVersion     string    `json:"reported_version"`
	Message             string    `json:"message"`
}

// NewCheckEventView converts an API check event.
func NewCheckEventView(e api.CheckEvent) CheckEventView {
	return CheckEventView{
		At: e.CreatedAt, Decision: e.Decision, Status: e.Status, DeploymentID: e.DeploymentID,
		OfferedBuildNumber: e.ArtifactBuildNumber, ReportedBuildNumber: e.ReportedBuildNumber,
		ReportedVersion: e.ReportedVersion, Message: e.Message,
	}
}

// CheckEventList prints device events.
func (p Printer) CheckEventList(events []CheckEventView) error {
	if p.JSON {
		return p.WriteJSON(events)
	}
	if len(events) == 0 {
		fmt.Fprintln(p.Out, "No checks recorded yet.")
		return nil
	}
	rows := make([][]string, len(events))
	for i, e := range events {
		rows[i] = []string{when(e.At), e.Status, intPtr(e.ReportedBuildNumber), intPtr(e.OfferedBuildNumber), e.Message}
	}
	return p.table([]string{"TIME", "RESULT", "RUNNING", "OFFERED", "MESSAGE"}, rows)
}
