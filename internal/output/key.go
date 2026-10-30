package output

import (
	"fmt"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"strings"
	"time"
)

// KeyView is the result of key generate.
type KeyView struct {
	KeyID             string   `json:"key_id"`
	ProjectID         string   `json:"project_id"`
	PublicKeyPEM      string   `json:"public_key_pem"`
	PrivateKeyFile    string   `json:"private_key_file"`
	DefaultSigningKey bool     `json:"default_signing_key"`
	FirmwareSnippet   string   `json:"firmware_snippet"`
	PinnedKeyIDs      []string `json:"pinned_key_ids"`
}

// KeyGenerated prints the result of key generate.
func (p Printer) KeyGenerated(v KeyView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	fmt.Fprintf(p.Out, "Registered signing key %q with the project.\n", v.KeyID)
	fmt.Fprintf(p.Out, "Encrypted private key: %s\n", v.PrivateKeyFile)
	if v.DefaultSigningKey {
		fmt.Fprintln(p.Out, "simpleota release signs with this key by default.")
	} else {
		fmt.Fprintf(p.Out, "Sign with it using: simpleota release --key-file %s ...\n", v.PrivateKeyFile)
	}
	fmt.Fprintf(p.Out, "\nPublic key:\n%s\n", v.PublicKeyPEM)
	if len(v.PinnedKeyIDs) == 1 {
		fmt.Fprintf(p.Out, "Pin it in your firmware (1.0 clients):\n\n%s", v.FirmwareSnippet)
		fmt.Fprintln(p.Out, "\nNext: run key generate again for an offline backup key, and pin both keys before your first signed release.")
		return nil
	}
	fmt.Fprintf(p.Out, "Pin %s in your firmware (1.0 clients):\n\n%s", strings.Join(v.PinnedKeyIDs, " and "), v.FirmwareSnippet)
	return nil
}

// ExportView is the result of key export.
type ExportView struct {
	Format     string `json:"format"`
	PrivateKey string `json:"private_key"`
}

// Exported prints the result of key export: the key itself, or its JSON.
func (p Printer) Exported(v ExportView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	_, err := fmt.Fprint(p.Out, v.PrivateKey)
	return err
}

// SigningKeyView is a public signing key registered on the project.
type SigningKeyView struct {
	KeyID        string     `json:"key_id"`
	Algorithm    string     `json:"algorithm"`
	PublicKeyPEM string     `json:"public_key_pem"`
	RevokedAt    *time.Time `json:"revoked_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// NewSigningKeyView converts an API signing key.
func NewSigningKeyView(k api.SigningKey) SigningKeyView {
	return SigningKeyView{KeyID: k.KeyID, Algorithm: k.Algorithm, PublicKeyPEM: k.PublicKeyPEM, RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt}
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

// RevokedView is the result of key revoke.
type RevokedView struct {
	KeyID     string `json:"key_id"`
	ProjectID string `json:"project_id"`
}

// Revoked prints the result of key revoke.
func (p Printer) Revoked(v RevokedView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	_, err := fmt.Fprintf(p.Out, "Revoked signing key %s. The server now refuses uploads signed with it.\n", v.KeyID)
	return err
}
