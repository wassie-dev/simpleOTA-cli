package output

import (
	"fmt"

	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

// Auth states in StatusView.
const (
	AuthValid       = "valid"
	AuthNotLoggedIn = "not_logged_in"
	AuthRejected    = "rejected"
	AuthUnreachable = "unreachable"
)

// StatusView is the result of status: the state of the CLI itself.
type StatusView struct {
	Version         string          `json:"version"`
	LatestVersion   string          `json:"latest_version"`
	UpdateAvailable bool            `json:"update_available"`
	UpdateError     string          `json:"update_error,omitempty"`
	APIURL          string          `json:"api_url"`
	ConfigDir       string          `json:"config_dir"`
	Auth            AuthView        `json:"auth"`
	Project         *ProjectView    `json:"project"`
	ProjectSource   string          `json:"project_source,omitempty"`
	ProjectError    string          `json:"project_error,omitempty"`
	SigningKey      SigningKeyState `json:"signing_key"`
}

// AuthView describes the token the CLI would use.
type AuthView struct {
	State     string       `json:"state"`
	TokenType string       `json:"token_type,omitempty"`
	Source    string       `json:"source,omitempty"`
	Account   *AccountView `json:"account,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// SigningKeyState describes the key release would sign with.
type SigningKeyState struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

// statusValueColumn is where values start in the status listing.
const statusValueColumn = 15

// projectSourceText says how the current project was chosen.
func projectSourceText(source string) string {
	switch source {
	case "":
		return ""
	case "project use":
		return ", chosen with 'simpleota project use'"
	case "the project token":
		return ", the project token's project"
	case "the only project":
		return ", the only project you have"
	}
	return ", from " + source
}

// Status prints the CLI's own status.
func (p Printer) Status(v StatusView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	update := "up to date"
	switch {
	case v.UpdateError != "":
		update = "could not check for updates: " + v.UpdateError
	case v.UpdateAvailable:
		update = p.yellow(v.LatestVersion+" is available") + "; run 'simpleota update'"
	}
	fmt.Fprintf(p.Out, "simpleota %s (%s)\n", v.Version, update)
	fmt.Fprintf(p.Out, "  api          %s\n", v.APIURL)

	switch v.Auth.State {
	case AuthValid:
		who := ""
		if a := v.Auth.Account; a != nil {
			who = " for " + termtext.Clean(a.Email)
			if a.IsStaff {
				who += " (staff)"
			}
		}
		fmt.Fprintf(p.Out, "  logged in    %s, %s token%s from %s\n", p.green("yes"), v.Auth.TokenType, who, v.Auth.Source)
	case AuthNotLoggedIn:
		fmt.Fprintf(p.Out, "  logged in    %s; run 'simpleota login', or set SIMPLEOTA_TOKEN\n", p.red("no"))
	case AuthRejected:
		fmt.Fprintf(p.Out, "  logged in    %s; the %s token from %s was rejected, so run 'simpleota login'\n", p.red("no"), v.Auth.TokenType, v.Auth.Source)
	case AuthUnreachable:
		fmt.Fprintf(p.Out, "  logged in    %s token from %s, not checked: %s\n", v.Auth.TokenType, v.Auth.Source, termtext.CleanLines(v.Auth.Error))
	}

	switch {
	case v.ProjectError != "":
		fmt.Fprintf(p.Out, "  project      %s\n", p.red(indentRest(termtext.CleanLines(v.ProjectError), statusValueColumn)))
	case v.Project != nil:
		fmt.Fprintf(p.Out, "  project      %s (%s)%s\n", termtext.Clean(v.Project.Slug), termtext.Clean(v.Project.ID), projectSourceText(v.ProjectSource))
	case v.Auth.State == AuthValid:
		fmt.Fprintln(p.Out, "  project      none selected; choose one with 'simpleota project use <slug>'")
	default:
		fmt.Fprintln(p.Out, "  project      none selected")
	}

	switch v.SigningKey.Source {
	case "file":
		key := v.SigningKey.Path
		if v.SigningKey.Error != "" {
			key += " " + p.red("("+indentRest(v.SigningKey.Error, statusValueColumn)+")")
		}
		fmt.Fprintf(p.Out, "  signing key  %s\n", key)
	case "env":
		fmt.Fprintln(p.Out, "  signing key  from SIMPLEOTA_SIGNING_KEY")
	default:
		fmt.Fprintln(p.Out, "  signing key  none; builds are uploaded unsigned (basic)")
	}
	fmt.Fprintf(p.Out, "  config       %s\n", v.ConfigDir)
	return nil
}
