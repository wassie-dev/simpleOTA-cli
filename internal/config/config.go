// Package config loads and saves the CLI's non-secret settings and validates
// the API base URL. Tokens live in the credentials package, never here.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DefaultAPIURL is the hosted SimpleOTA service.
const DefaultAPIURL = "https://simpleota.com"

const fileName = "config.json"

// Config is persisted as JSON in the config directory with 0600 permissions.
type Config struct {
	APIURL         string `json:"api_url,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	ProjectSlug    string `json:"project_slug,omitempty"`
	SigningKeyFile string `json:"signing_key_file,omitempty"`
}

// Dir returns the directory holding config, the token fallback file and
// generated keys. SIMPLEOTA_CONFIG_DIR overrides the OS default.
func Dir(getenv func(string) string) (string, error) {
	if d := getenv("SIMPLEOTA_CONFIG_DIR"); d != "" {
		return filepath.Clean(d), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the config directory: %w (set SIMPLEOTA_CONFIG_DIR)", err)
	}
	return filepath.Join(base, "simpleota"), nil
}

// Load reads the config file. A missing file yields an empty Config.
func Load(dir string) (Config, error) {
	var c Config
	data, err := os.ReadFile(filepath.Join(dir, fileName)) // #nosec G304 -- the CLI's own config directory
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("reading config: %w", err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("config file %s is not valid JSON: %w", filepath.Join(dir, fileName), err)
	}
	return c, nil
}

// Save writes the config atomically with 0600 permissions.
func Save(dir string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(filepath.Join(dir, fileName), append(data, '\n'))
}

// WriteFileAtomic writes data to path via a temp file and rename, creating the
// parent directory with 0700 and the file with 0600.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// NormalizeAPIURL validates an API base URL and returns it as scheme://host[:port].
// HTTPS is required except for loopback hosts, which may use plain HTTP so the
// CLI can talk to a local development server.
func NormalizeAPIURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid API URL %q: expected something like %s", raw, DefaultAPIURL)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", fmt.Errorf("invalid API URL %q: give only the scheme and host, like %s", raw, DefaultAPIURL)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !IsLoopback(u.Hostname()) {
			return "", fmt.Errorf("refusing plain http for %s: use https (http is allowed only for localhost)", u.Host)
		}
	default:
		return "", fmt.Errorf("invalid API URL %q: scheme must be https", raw)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// IsLoopback reports whether host is localhost or a loopback IP literal.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
