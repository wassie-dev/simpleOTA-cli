package output

import "testing"

func TestAccountGolden(t *testing.T) {
	views := map[string]any{
		"login.json":  LoginView{APIURL: "https://simpleota.com", TokenType: "project", CredentialStore: "the OS keychain", Project: ProjectView{ID: "p", Slug: "fleet", Name: "Fleet"}},
		"logout.json": LogoutView{APIURL: "https://simpleota.com", LoggedOut: true},
	}
	for name, v := range views {
		golden(t, name, render(t, true, func(p Printer) error { return p.WriteJSON(v) }))
	}
}
