package output

import "testing"

func TestStatusGolden(t *testing.T) {
	v := StatusView{
		Version: "0.1.0", LatestVersion: "0.2.0", UpdateAvailable: true,
		APIURL: "https://simpleota.com", ConfigDir: "/home/me/.config/simpleota",
		Auth:          AuthView{State: AuthValid, TokenType: "personal", Source: "the OS keychain", Account: &AccountView{ID: 7, Email: "me@example.com", Username: "me"}},
		Project:       &ProjectView{ID: "11111111-1111-4111-8111-111111111111", Slug: "fleet", Name: "Fleet"},
		ProjectSource: "project use",
		SigningKey:    SigningKeyState{Source: "file", Path: "/home/me/.config/simpleota/keys/prod-2026.key"},
	}
	golden(t, "cli_status.json", render(t, true, func(p Printer) error { return p.Status(v) }))
	golden(t, "cli_status.txt", render(t, false, func(p Printer) error { return p.Status(v) }))
}
