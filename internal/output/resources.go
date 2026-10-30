package output

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// ProjectView is a project. Fields other than id, slug and name are empty
// where the command did not fetch them.
type ProjectView struct {
	ID               string     `json:"id"`
	Slug             string     `json:"slug"`
	Name             string     `json:"name"`
	Description      string     `json:"description,omitempty"`
	DefaultFramework string     `json:"default_framework,omitempty"`
	BuildCounter     int        `json:"build_counter,omitempty"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
	// Current is set in project list on the project commands act on.
	Current bool `json:"current,omitempty"`
}

// NewProjectView converts an API project.
func NewProjectView(p api.Project) ProjectView {
	v := ProjectView{ID: p.ID, Slug: p.Slug, Name: p.Name, Description: p.Description, DefaultFramework: p.DefaultFramework, BuildCounter: p.BuildCounter}
	if !p.CreatedAt.IsZero() {
		t := p.CreatedAt
		v.CreatedAt = &t
	}
	return v
}

// ProjectList prints project list.
func (p Printer) ProjectList(projects []ProjectView) error {
	if p.JSON {
		return p.WriteJSON(projects)
	}
	if len(projects) == 0 {
		fmt.Fprintln(p.Out, "No projects. Create one in the dashboard.")
		return nil
	}
	rows := make([][]string, len(projects))
	for i, pr := range projects {
		mark := " "
		if pr.Current {
			mark = "*"
		}
		rows[i] = []string{mark, pr.Slug, pr.Name, pr.ID}
	}
	return p.table([]string{"", "SLUG", "NAME", "ID"}, rows)
}

// ProjectUseView is the result of project use.
type ProjectUseView struct {
	APIURL  string      `json:"api_url"`
	Project ProjectView `json:"project"`
}

// ProjectUse prints the result of project use.
func (p Printer) ProjectUse(v ProjectUseView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	fmt.Fprintf(p.Out, "Commands against %s now act on %s (%s).\n", v.APIURL, v.Project.Slug, v.Project.Name)
	return nil
}

// ProjectShow prints project show.
func (p Printer) ProjectShow(v ProjectView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	fmt.Fprintf(p.Out, "Project %s\n", p.bold(v.Slug))
	p.field("name", v.Name)
	p.field("id", v.ID)
	p.field("framework", v.DefaultFramework)
	if v.BuildCounter > 0 {
		p.field("last build", strconv.Itoa(v.BuildCounter))
	}
	p.field("created", whenPtr(v.CreatedAt))
	p.field("description", v.Description)
	return nil
}

// field prints one indented "label  value" line, skipping empty values.
func (p Printer) field(label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(p.Out, "  %-12s %s\n", label, value)
}

// BuildView is a firmware build, which the API calls an artifact.
type BuildView struct {
	BuildNumber      int       `json:"build_number"`
	ID               string    `json:"id"`
	VersionLabel     string    `json:"version_label"`
	Framework        string    `json:"framework"`
	Chip             string    `json:"chip"`
	BoardID          string    `json:"board_id"`
	PartitionProfile string    `json:"partition_profile"`
	NVSSchemaVersion *int      `json:"nvs_schema_version"`
	SecurityMode     string    `json:"security_mode"`
	SHA256           string    `json:"sha256"`
	Size             int64     `json:"size"`
	Archived         bool      `json:"archived"`
	CreatedAt        time.Time `json:"created_at"`
	Warnings         []string  `json:"warnings"`
}

// NewBuildView converts an API artifact.
func NewBuildView(a api.Artifact) BuildView {
	warnings := a.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return BuildView{
		BuildNumber: a.BuildNumber, ID: a.ID, VersionLabel: a.VersionLabel, Framework: a.Framework,
		Chip: a.ChipFamily, BoardID: a.BoardID, PartitionProfile: a.PartitionProfile, NVSSchemaVersion: a.NVSSchemaVersion,
		SecurityMode: a.SecurityMode, SHA256: a.ChecksumSHA256, Size: a.FileSize, Archived: a.Archived,
		CreatedAt: a.CreatedAt, Warnings: warnings,
	}
}

// BuildList prints build list.
func (p Printer) BuildList(builds []BuildView) error {
	if p.JSON {
		return p.WriteJSON(builds)
	}
	if len(builds) == 0 {
		fmt.Fprintln(p.Out, "No builds yet. Upload one with: simpleota release <bin> --version-label <label>")
		return nil
	}
	rows := make([][]string, len(builds))
	for i, b := range builds {
		state := "ready"
		if b.Archived {
			state = "archived"
		}
		rows[i] = []string{strconv.Itoa(b.BuildNumber), b.VersionLabel, b.Chip, b.SecurityMode, state, when(b.CreatedAt)}
	}
	return p.table([]string{"BUILD", "VERSION", "CHIP", "SECURITY", "STATE", "CREATED"}, rows)
}

// BuildShow prints build show.
func (p Printer) BuildShow(b BuildView) error {
	if p.JSON {
		return p.WriteJSON(b)
	}
	fmt.Fprintf(p.Out, "Build %s\n", p.bold(strconv.Itoa(b.BuildNumber)))
	p.field("version", b.VersionLabel)
	p.field("id", b.ID)
	p.field("framework", b.Framework)
	p.field("chip", b.Chip)
	p.field("board", b.BoardID)
	p.field("partitions", b.PartitionProfile)
	if b.NVSSchemaVersion != nil {
		p.field("nvs schema", strconv.Itoa(*b.NVSSchemaVersion))
	}
	p.field("security", b.SecurityMode)
	p.field("sha256", b.SHA256)
	p.field("size", fmt.Sprintf("%d bytes", b.Size))
	if b.Archived {
		p.field("state", p.yellow("archived"))
	} else {
		p.field("state", "ready")
	}
	p.field("created", when(b.CreatedAt))
	for _, w := range b.Warnings {
		p.field("warning", p.yellow(w))
	}
	return nil
}

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

func intPtr(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

// ChannelView is a release channel.
type ChannelView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
}

// ChannelShow prints one channel, as channel create does.
func (p Printer) ChannelShow(c ChannelView) error {
	if p.JSON {
		return p.WriteJSON(c)
	}
	fmt.Fprintf(p.Out, "Channel %s\n", p.bold(c.Name))
	p.field("id", c.ID)
	if c.IsDefault {
		p.field("default", "yes; new devices join it")
	}
	p.field("description", c.Description)
	return nil
}

// ChannelList prints channel list.
func (p Printer) ChannelList(channels []ChannelView) error {
	if p.JSON {
		return p.WriteJSON(channels)
	}
	rows := make([][]string, len(channels))
	for i, c := range channels {
		def := ""
		if c.IsDefault {
			def = "yes"
		}
		rows[i] = []string{c.Name, def, c.Description, c.ID}
	}
	return p.table([]string{"NAME", "DEFAULT", "DESCRIPTION", "ID"}, rows)
}

// SigningKeyView is a public signing key registered on the project.
type SigningKeyView struct {
	KeyID        string     `json:"key_id"`
	Algorithm    string     `json:"algorithm"`
	PublicKeyPEM string     `json:"public_key_pem"`
	RevokedAt    *time.Time `json:"revoked_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// KeyList prints key list.
func (p Printer) KeyList(keys []SigningKeyView) error {
	if p.JSON {
		return p.WriteJSON(keys)
	}
	if len(keys) == 0 {
		fmt.Fprintln(p.Out, "No signing keys. Create one with: simpleota key generate --key-id <id>")
		return nil
	}
	rows := make([][]string, len(keys))
	for i, k := range keys {
		state := "active"
		if k.RevokedAt != nil {
			state = "revoked " + whenPtr(k.RevokedAt)
		}
		rows[i] = []string{k.KeyID, k.Algorithm, state, when(k.CreatedAt)}
	}
	return p.table([]string{"KEY ID", "ALGORITHM", "STATE", "CREATED"}, rows)
}

// DeploymentList prints deployment list.
func (p Printer) DeploymentList(deps []DeploymentView) error {
	if p.JSON {
		return p.WriteJSON(deps)
	}
	if len(deps) == 0 {
		fmt.Fprintln(p.Out, "No deployments yet. Create one with: simpleota release <bin> --version-label <label>")
		return nil
	}
	rows := make([][]string, len(deps))
	for i, d := range deps {
		rows[i] = []string{d.ID, strconv.Itoa(d.BuildNumber), d.State, fmt.Sprintf("%d%%", d.RolloutPercentage), when(d.CreatedAt)}
	}
	return p.table([]string{"DEPLOYMENT", "BUILD", "STATE", "ROLLOUT", "CREATED"}, rows)
}

// GroupView is a device group.
type GroupView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// GroupList prints group list.
func (p Printer) GroupList(groups []GroupView) error {
	if p.JSON {
		return p.WriteJSON(groups)
	}
	if len(groups) == 0 {
		fmt.Fprintln(p.Out, "No device groups. Create one with: simpleota group create <name>")
		return nil
	}
	rows := make([][]string, len(groups))
	for i, g := range groups {
		rows[i] = []string{g.Name, g.Description, g.ID}
	}
	return p.table([]string{"NAME", "DESCRIPTION", "ID"}, rows)
}

// GroupShow prints one group, as group create does.
func (p Printer) GroupShow(g GroupView) error {
	if p.JSON {
		return p.WriteJSON(g)
	}
	fmt.Fprintf(p.Out, "Group %s\n", p.bold(g.Name))
	p.field("id", g.ID)
	p.field("description", g.Description)
	return nil
}

// MembersView is the result of group member add and remove.
type MembersView struct {
	Group   string   `json:"group"`
	Devices []string `json:"devices"`
	Added   bool     `json:"added"`
}

// MembersChanged prints the result of group member add and remove.
func (p Printer) MembersChanged(v MembersView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	verb, prep := "Added", "to"
	if !v.Added {
		verb, prep = "Removed", "from"
	}
	fmt.Fprintf(p.Out, "%s %s %s group %s.\n", verb, strings.Join(v.Devices, ", "), prep, v.Group)
	return nil
}

// AuditView is one audit log entry.
type AuditView struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	EventType  string         `json:"event_type"`
	ActorID    *int           `json:"actor_id"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Payload    map[string]any `json:"payload"`
}

// AuditList prints audit.
func (p Printer) AuditList(events []AuditView) error {
	if p.JSON {
		return p.WriteJSON(events)
	}
	if len(events) == 0 {
		fmt.Fprintln(p.Out, "No audit events.")
		return nil
	}
	rows := make([][]string, len(events))
	for i, e := range events {
		actor := "system"
		if e.ActorID != nil {
			actor = "user " + strconv.Itoa(*e.ActorID)
		}
		rows[i] = []string{when(e.At), e.EventType, actor, e.TargetType + " " + e.TargetID, payloadText(e.Payload)}
	}
	return p.table([]string{"TIME", "EVENT", "BY", "TARGET", "DETAILS"}, rows)
}

// payloadText renders an event payload as sorted key=value pairs.
func payloadText(payload map[string]any) string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, payload[k])
	}
	return strings.Join(parts, " ")
}
