package output

import (
	"fmt"
	"strconv"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/release"
)

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

// UploadView is the result of build upload.
type UploadView struct {
	BuildNumber  int    `json:"build_number"`
	ArtifactID   string `json:"artifact_id"`
	SecurityMode string `json:"security_mode"`
	KeyID        string `json:"key_id,omitempty"`
	Chip         string `json:"chip"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

// NewUploadView converts an upload result.
func NewUploadView(r release.Result) UploadView {
	return UploadView{BuildNumber: r.BuildNumber, ArtifactID: r.ArtifactID, SecurityMode: r.SecurityMode, KeyID: r.KeyID, Chip: r.Chip, SHA256: r.SHA256, Size: r.Size}
}

// Upload prints the result of build upload.
func (p Printer) Upload(r UploadView) error {
	if p.JSON {
		return p.WriteJSON(r)
	}
	fmt.Fprintf(p.Out, "%s build %d for %s (%s)\n", p.green("Uploaded"), r.BuildNumber, r.Chip, securityText(r.SecurityMode, r.KeyID))
	fmt.Fprintf(p.Out, "  sha256      %s (%d bytes, checked by the server)\n", r.SHA256, r.Size)
	fmt.Fprintf(p.Out, "Deploy it with: simpleota deployment create --build %d\n", r.BuildNumber)
	return nil
}

func securityText(mode, keyID string) string {
	if keyID != "" {
		return mode + " with key " + keyID
	}
	return mode
}
