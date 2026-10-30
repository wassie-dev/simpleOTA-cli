package output

import "testing"

func TestKeysGolden(t *testing.T) {
	views := map[string]any{
		"key.json":    KeyView{KeyID: "prod-2026", ProjectID: "p", PublicKeyPEM: "PEM", PrivateKeyFile: "/k", DefaultSigningKey: true, FirmwareSnippet: "snippet", PinnedKeyIDs: []string{"prod-2026"}},
		"export.json": ExportView{Format: "pem", PrivateKey: "PEM"},
	}
	for name, v := range views {
		golden(t, name, render(t, true, func(p Printer) error { return p.WriteJSON(v) }))
	}
}
