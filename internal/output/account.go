package output

import (
	"fmt"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// AccountView is the account a personal token belongs to.
type AccountView struct {
	ID       int    `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	IsStaff  bool   `json:"is_staff"`
}

// NewAccountView converts an API account; nil stays nil.
func NewAccountView(a *api.Account) *AccountView {
	if a == nil {
		return nil
	}
	return &AccountView{ID: a.ID, Email: a.Email, Username: a.Username, IsStaff: a.IsStaff}
}

// LoginView is the result of login.
type LoginView struct {
	APIURL          string       `json:"api_url"`
	TokenType       string       `json:"token_type"`
	CredentialStore string       `json:"credential_store"`
	Account         *AccountView `json:"account"`
	// Project is the project commands now act on: a project token's own
	// project, or a personal token's only project. Null otherwise.
	Project *ProjectView `json:"project"`
	// ProjectCount is how many projects the token can see, or -1 when the
	// server would not list them.
	ProjectCount int `json:"project_count"`
}

// Login prints the result of login.
func (p Printer) Login(v LoginView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	switch {
	case v.TokenType == "project" && v.Project != nil:
		fmt.Fprintf(p.Out, "Logged in to %s with a project token for %s.\n", v.APIURL, v.Project.Slug)
	case v.Account != nil:
		fmt.Fprintf(p.Out, "Logged in to %s as %s (%s token).\n", v.APIURL, v.Account.Email, v.TokenType)
	default:
		fmt.Fprintf(p.Out, "Logged in to %s with a %s token.\n", v.APIURL, v.TokenType)
	}
	fmt.Fprintf(p.Out, "Token stored in %s.\n", v.CredentialStore)
	if v.TokenType == "project" {
		return nil
	}
	switch {
	case v.ProjectCount == 1 && v.Project != nil:
		fmt.Fprintf(p.Out, "Commands act on %s, the only project this account has.\n", v.Project.Slug)
	case v.ProjectCount == 0:
		fmt.Fprintln(p.Out, "This account has no projects yet; create one in the dashboard.")
	case v.ProjectCount < 0:
		fmt.Fprintln(p.Out, "Choose a project with: simpleota project use <project-id>")
	default:
		fmt.Fprintf(p.Out, "This account has %d projects. Choose one with: simpleota project use <slug>\n", v.ProjectCount)
		fmt.Fprintln(p.Out, "List them with: simpleota project list")
	}
	return nil
}

// LogoutView is the result of logout.
type LogoutView struct {
	APIURL    string `json:"api_url"`
	LoggedOut bool   `json:"logged_out"`
}
