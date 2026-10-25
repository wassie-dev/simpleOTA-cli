// Package credentials stores API tokens in the OS keychain, with a 0600 file
// as the fallback when no keychain is available.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/zalando/go-keyring"

	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/fileperm"
)

// ErrNotFound means no token is stored for the API URL.
var ErrNotFound = errors.New("no stored token")

// Kind is the type of SimpleOTA API token.
type Kind string

const (
	KindPersonal Kind = "personal"
	KindProject  Kind = "project"
)

const (
	personalPrefix = "soto_pat_"
	projectPrefix  = "soto_proj_"
)

var tokenPattern = regexp.MustCompile(`^(soto_pat_|soto_proj_)[A-Za-z0-9_-]{16,200}$`)

// Classify validates the token's shape and reports its kind.
func Classify(token string) (Kind, error) {
	if !tokenPattern.MatchString(token) {
		return "", errors.New("that is not a SimpleOTA API token: expected a personal token (soto_pat_...) or a project token (soto_proj_...) from the dashboard")
	}
	if token[:len(personalPrefix)] == personalPrefix {
		return KindPersonal, nil
	}
	return KindProject, nil
}

// Store keeps one token per API URL.
type Store interface {
	Get(apiURL string) (string, error)
	Set(apiURL, token string) error
	Delete(apiURL string) error
	// Name describes where tokens are kept, for messages.
	Name() string
}

const keyringService = "simpleota"

// Keyring stores tokens in the OS keychain (macOS Keychain, Windows
// Credential Manager, or the Secret Service on Linux).
type Keyring struct{}

func (Keyring) Name() string { return "the OS keychain" }

func (Keyring) Get(apiURL string) (string, error) {
	t, err := keyring.Get(keyringService, apiURL)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return t, err
}

func (Keyring) Set(apiURL, token string) error {
	return keyring.Set(keyringService, apiURL, token)
}

func (Keyring) Delete(apiURL string) error {
	err := keyring.Delete(keyringService, apiURL)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// File stores tokens as JSON in a file readable only by the owner.
type File struct {
	Path string
}

func (f File) Name() string { return f.Path }

func (f File) load() (map[string]string, error) {
	tokens := map[string]string{}
	if err := fileperm.CheckPrivate(f.Path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return tokens, nil
		}
		return nil, err
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", f.Path, err)
	}
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("token file %s is corrupt; delete it and run simpleota login again", f.Path)
	}
	return tokens, nil
}

func (f File) Get(apiURL string) (string, error) {
	tokens, err := f.load()
	if err != nil {
		return "", err
	}
	t, ok := tokens[apiURL]
	if !ok {
		return "", ErrNotFound
	}
	return t, nil
}

func (f File) Set(apiURL, token string) error {
	tokens, err := f.load()
	if err != nil {
		return err
	}
	tokens[apiURL] = token
	return f.save(tokens)
}

func (f File) Delete(apiURL string) error {
	tokens, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := tokens[apiURL]; !ok {
		return nil
	}
	delete(tokens, apiURL)
	if len(tokens) == 0 {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return f.save(tokens)
}

func (f File) save(tokens map[string]string) error {
	data, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(f.Path, data)
}

// Fallback tries Primary first and uses Secondary when Primary is unavailable.
type Fallback struct {
	Primary   Store
	Secondary Store
}

func (s Fallback) Name() string { return s.Primary.Name() }

// Get reports a keychain failure rather than "not found" when the file has
// no token either, so a locked or broken keychain is not mistaken for a
// logged-out state.
func (s Fallback) Get(apiURL string) (string, error) {
	t, err := s.Primary.Get(apiURL)
	if err == nil {
		return t, nil
	}
	t, errS := s.Secondary.Get(apiURL)
	if errS == nil {
		return t, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", fmt.Errorf("reading %s: %w", s.Primary.Name(), err)
	}
	return "", errS
}

func (s Fallback) Set(apiURL, token string) error {
	_, err := s.SetWhere(apiURL, token)
	return err
}

// SetWhere stores the token and returns the name of the store that holds it.
func (s Fallback) SetWhere(apiURL, token string) (string, error) {
	if err := s.Primary.Set(apiURL, token); err == nil {
		// Drop any older copy so the keychain is the single source.
		_ = s.Secondary.Delete(apiURL)
		return s.Primary.Name(), nil
	}
	if err := s.Secondary.Set(apiURL, token); err != nil {
		return "", err
	}
	return s.Secondary.Name(), nil
}

func (s Fallback) Delete(apiURL string) error {
	errP := s.Primary.Delete(apiURL)
	errS := s.Secondary.Delete(apiURL)
	if errP != nil && errS != nil {
		return errS
	}
	return nil
}

// Default returns the keychain with a file fallback in configDir. With
// noKeyring set, only the file is used.
func Default(configDir string, noKeyring bool) Store {
	file := File{Path: filepath.Join(configDir, "credentials.json")}
	if noKeyring {
		return file
	}
	return Fallback{Primary: Keyring{}, Secondary: file}
}

// Redact shortens a token to its non-secret prefix for display.
func Redact(token string) string {
	switch {
	case len(token) > len(projectPrefix) && token[:len(projectPrefix)] == projectPrefix:
		return projectPrefix + "…"
	case len(token) > len(personalPrefix) && token[:len(personalPrefix)] == personalPrefix:
		return personalPrefix + "…"
	default:
		return "…"
	}
}
