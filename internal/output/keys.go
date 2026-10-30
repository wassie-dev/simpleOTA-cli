package output

import "fmt"

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

// ExportView is the result of key export.
type ExportView struct {
	Format     string `json:"format"`
	PrivateKey string `json:"private_key"`
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
