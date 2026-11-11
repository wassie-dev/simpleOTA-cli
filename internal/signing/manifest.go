package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ManifestV1Magic is the first line of a version 1 manifest. No firmware
// image starts with it (an ESP32 image starts with 0xE9), so an image
// signature and a manifest signature can never pass for each other.
const ManifestV1Magic = "SimpleOTA-Manifest-v1"

// ManifestVersion is the manifest version this CLI signs.
const ManifestVersion = 1

// Manifest names what a build is: the project it belongs to, the key that
// signed its image, the build number the server gave it and the image's
// SHA-256. Signing it binds the build number, which the image signature
// cannot cover because the server assigns it after the upload.
type Manifest struct {
	// ProjectID is the project's UUID, as the dashboard shows it and as
	// devices are configured with.
	ProjectID string
	KeyID     string
	Build     int
	SHA256    string
}

// Bytes returns the version 1 manifest: five lines, each ended by one
// newline. Devices rebuild these bytes from what they hold and verify the
// signature over them, so the format is fixed.
func (m Manifest) Bytes() ([]byte, error) {
	switch {
	case m.ProjectID == "":
		return nil, errors.New("the manifest needs a project ID")
	case hasControl(m.ProjectID):
		return nil, fmt.Errorf("project ID %q has a control character", m.ProjectID)
	case hasControl(m.KeyID):
		return nil, fmt.Errorf("key ID %q has a control character", m.KeyID)
	case m.Build < 1:
		return nil, fmt.Errorf("build number %d is not valid", m.Build)
	case !isSHA256Hex(m.SHA256):
		return nil, fmt.Errorf("%q is not a SHA-256 in hex", m.SHA256)
	}
	lines := []string{ManifestV1Magic, m.ProjectID, m.KeyID, strconv.Itoa(m.Build), strings.ToLower(m.SHA256)}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// SignManifest returns the base64 Ed25519 signature over the manifest.
func SignManifest(priv ed25519.PrivateKey, m Manifest) (string, error) {
	msg, err := m.Bytes()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)), nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range strings.ToLower(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
