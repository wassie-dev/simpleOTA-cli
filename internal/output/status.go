package output

import "fmt"

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
	SigningKey      SigningKeyState `json:"signing_key"`
}

// AuthView describes the token the CLI would use.
type AuthView struct {
	State     string `json:"state"`
	TokenType string `json:"token_type,omitempty"`
	Source    string `json:"source,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SigningKeyState describes the key release would sign with.
type SigningKeyState struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
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
		update = p.yellow(v.LatestVersion+" is available") + "; run simpleota update"
	}
	fmt.Fprintf(p.Out, "simpleota %s (%s)\n", v.Version, update)
	fmt.Fprintf(p.Out, "  api          %s\n", v.APIURL)

	switch v.Auth.State {
	case AuthValid:
		fmt.Fprintf(p.Out, "  logged in    %s, %s token from %s\n", p.green("yes"), v.Auth.TokenType, v.Auth.Source)
	case AuthNotLoggedIn:
		fmt.Fprintf(p.Out, "  logged in    %s; run simpleota login, or set SIMPLEOTA_TOKEN\n", p.red("no"))
	case AuthRejected:
		fmt.Fprintf(p.Out, "  logged in    %s: the %s token from %s was rejected; run simpleota login\n", p.red("no"), v.Auth.TokenType, v.Auth.Source)
	case AuthUnreachable:
		fmt.Fprintf(p.Out, "  logged in    %s token from %s, not checked: %s\n", v.Auth.TokenType, v.Auth.Source, v.Auth.Error)
	}

	if v.Project != nil {
		fmt.Fprintf(p.Out, "  project      %s (%s)\n", v.Project.Slug, v.Project.ID)
	} else {
		fmt.Fprintln(p.Out, "  project      none selected")
	}

	switch v.SigningKey.Source {
	case "file":
		key := v.SigningKey.Path
		if v.SigningKey.Error != "" {
			key += " " + p.red("("+v.SigningKey.Error+")")
		}
		fmt.Fprintf(p.Out, "  signing key  %s\n", key)
	case "env":
		fmt.Fprintln(p.Out, "  signing key  from SIMPLEOTA_SIGNING_KEY")
	default:
		fmt.Fprintln(p.Out, "  signing key  none; releases are unsigned (basic)")
	}
	fmt.Fprintf(p.Out, "  config       %s\n", v.ConfigDir)
	return nil
}
