package output

import "testing"

func TestAccountGolden(t *testing.T) {
	views := map[string]any{
		"login.json":  LoginView{APIURL: "https://simpleota.com", TokenType: "personal", CredentialStore: "the OS keychain", Account: &AccountView{ID: 7, Email: "me@example.com", Username: "me"}, Project: &ProjectView{ID: "p", Slug: "fleet", Name: "Fleet"}, ProjectCount: 1},
		"logout.json": LogoutView{APIURL: "https://simpleota.com", LoggedOut: true},
	}
	for name, v := range views {
		golden(t, name, render(t, true, func(p Printer) error { return p.WriteJSON(v) }))
	}
}
