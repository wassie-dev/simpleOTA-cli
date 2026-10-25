package output

// ProjectView identifies a project.
type ProjectView struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// LoginView is the result of login.
type LoginView struct {
	APIURL          string      `json:"api_url"`
	TokenType       string      `json:"token_type"`
	CredentialStore string      `json:"credential_store"`
	Project         ProjectView `json:"project"`
}

// LogoutView is the result of logout.
type LogoutView struct {
	APIURL    string `json:"api_url"`
	LoggedOut bool   `json:"logged_out"`
}
