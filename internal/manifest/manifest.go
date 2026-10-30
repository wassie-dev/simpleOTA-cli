// Package manifest builds and validates the sidecar manifest sent with every
// firmware upload. Field rules mirror the server's FirmwareManifest schema.
package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	FrameworkArduino = "arduino"
	FrameworkESPIDF  = "esp_idf"

	SecurityBasic  = "basic"
	SecuritySigned = "signed"

	AlgorithmEd25519 = "ed25519"
)

var (
	chipPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	keyIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Manifest is the JSON document the server validates before allocating a
// build number.
type Manifest struct {
	Framework       string           `json:"framework"`
	VersionLabel    string           `json:"version_label"`
	ChipFamily      string           `json:"chip_family"`
	SecurityMode    string           `json:"security_mode"`
	SigningMetadata *SigningMetadata `json:"signing_metadata,omitempty"`
	// SHA256 is the binary's digest in lower-case hex. The server rejects
	// the upload, before creating anything, when the bytes it received
	// hash differently.
	SHA256 string `json:"sha256,omitempty"`
}

// SigningMetadata carries the detached Ed25519 signature over the binary.
type SigningMetadata struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

// ValidKeyID reports whether id is a key id the server accepts.
func ValidKeyID(id string) bool {
	return keyIDPattern.MatchString(id)
}

// NormalizeChip lower-cases and validates a chip family such as esp32s3.
func NormalizeChip(chip string) (string, error) {
	c := strings.ToLower(strings.TrimSpace(chip))
	if !chipPattern.MatchString(c) {
		return "", fmt.Errorf("invalid chip %q: use the chip family the device reports, such as esp32, esp32s3 or esp32c3", chip)
	}
	return c, nil
}

// Validate checks the manifest against the server's rules so a bad upload
// fails locally instead of on the server.
func (m Manifest) Validate() error {
	if m.Framework != FrameworkArduino && m.Framework != FrameworkESPIDF {
		return fmt.Errorf("invalid framework %q: use %s or %s", m.Framework, FrameworkArduino, FrameworkESPIDF)
	}
	if err := validateVersionLabel(m.VersionLabel); err != nil {
		return err
	}
	if !chipPattern.MatchString(m.ChipFamily) {
		return fmt.Errorf("invalid chip %q", m.ChipFamily)
	}
	if m.SHA256 != "" && !sha256Pattern.MatchString(m.SHA256) {
		return fmt.Errorf("invalid sha256 %q: use 64 lower-case hexadecimal characters", m.SHA256)
	}
	switch m.SecurityMode {
	case SecurityBasic:
		if m.SigningMetadata != nil {
			return errors.New("a basic release cannot carry a signature")
		}
	case SecuritySigned:
		s := m.SigningMetadata
		if s == nil || s.Signature == "" || !ValidKeyID(s.KeyID) || s.Algorithm != AlgorithmEd25519 {
			return errors.New("a signed release needs a key id, an Ed25519 signature and the algorithm")
		}
	default:
		return fmt.Errorf("invalid security mode %q: use %s or %s", m.SecurityMode, SecurityBasic, SecuritySigned)
	}
	return nil
}

func validateVersionLabel(v string) error {
	if strings.TrimSpace(v) != v || v == "" {
		return errors.New("the version label must be non-empty with no leading or trailing spaces")
	}
	if utf8.RuneCountInString(v) > 64 {
		return errors.New("the version label must be at most 64 characters")
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return errors.New("the version label must not contain control characters")
		}
	}
	return nil
}
