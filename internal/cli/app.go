// Package cli wires the cobra commands to the business packages. Handlers
// stay thin: they parse input, call a package, and render the result.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/update"
	"github.com/wassie-dev/simpleOTA-cli/internal/version"
)

// Help groups on the root command.
const (
	groupCLI       = "cli"
	groupProject   = "project"
	groupResources = "resources"
)

// Exit codes.
const (
	ExitOK         = 0
	ExitError      = 1
	ExitUsage      = 2
	ExitAuth       = 3
	ExitDeployment = 4
	ExitTimeout    = 5
)

// exitError carries a specific exit code. A quiet one is not printed,
// because the command's output already explains it.
type exitError struct {
	code  int
	err   error
	quiet bool
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// App holds the process environment so commands are testable without
// touching the real terminal, keychain or network.
type App struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Getenv func(string) string

	// Store overrides the credential store; nil selects the default.
	Store credentials.Store
	// NewAPI builds the API client; nil selects the HTTP client.
	NewAPI func(baseURL, token string) api.API
	// Prompt reads a secret from the terminal; nil disables prompting.
	Prompt func(label string) (string, error)
	// StdinIsTerminal reports whether In is an interactive terminal.
	StdinIsTerminal bool
	// Color enables ANSI colour in human output.
	Color bool

	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error

	// CurrentVersion, Executable and UpdateSource override how update finds
	// this binary and new releases; nil selects the real ones.
	CurrentVersion func() string
	Executable     func() (string, error)
	UpdateSource   *update.Source

	flags globalFlags
}

type globalFlags struct {
	json    bool
	apiURL  string
	project string
}

// NewApp returns an App bound to the real process.
func NewApp() *App {
	stdinTTY := term.IsTerminal(int(os.Stdin.Fd()))
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	a := &App{
		In:              os.Stdin,
		Out:             os.Stdout,
		Err:             os.Stderr,
		Getenv:          os.Getenv,
		StdinIsTerminal: stdinTTY,
		Color:           stdoutTTY && os.Getenv("NO_COLOR") == "",
		Now:             time.Now,
		Sleep:           sleepCtx,
	}
	if stdinTTY {
		a.Prompt = terminalPrompt(os.Stdin, os.Stderr)
	}
	return a
}

func terminalPrompt(in *os.File, w io.Writer) func(string) (string, error) {
	return func(label string) (string, error) {
		fmt.Fprint(w, label)
		b, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(w)
		if err != nil {
			return "", fmt.Errorf("reading input: %w", err)
		}
		return string(b), nil
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (a *App) printer() output.Printer {
	return output.Printer{Out: a.Out, Err: a.Err, JSON: a.flags.json, Color: a.Color && !a.flags.json}
}

// Run executes the CLI with args and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.rootCommand()
	root.SetArgs(args)
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	code := exitCode(err)
	var ee *exitError
	if errors.As(err, &ee) && ee.quiet {
		return code
	}
	msg := message(err)
	if a.flags.json {
		_ = a.printer().WriteJSON(output.ErrorView{Error: msg, ExitCode: code})
	} else {
		fmt.Fprintln(a.Err, "Error: "+msg)
	}
	return code
}

// message explains the errors every command can hit in terms of what to do.
func message(err error) string {
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case http.StatusUnauthorized:
			return "SimpleOTA rejected the token (401): it may be revoked or mistyped; run simpleota login, or check SIMPLEOTA_TOKEN"
		case http.StatusForbidden:
			return "SimpleOTA refused this for the token (403): project tokens reach only their own project, and some actions need a personal token"
		}
	}
	return err.Error()
}

func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	var ae *api.Error
	if errors.As(err, &ae) && ae.IsAuth() {
		return ExitAuth
	}
	// cobra reports an unknown top-level command as a plain error.
	if strings.HasPrefix(err.Error(), "unknown command") {
		return ExitUsage
	}
	return ExitError
}

func (a *App) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "simpleota",
		Short: "Sign, release and roll out ESP32 firmware with SimpleOTA",
		Long: `simpleota signs, uploads and rolls out ESP32 firmware through SimpleOTA.

Commands follow the API's resources. login stores a token, and a project
holds your fleet: its builds, deployments, devices, channels, groups,
signing keys and audit log. Resource commands act on the current project:
--project, then SIMPLEOTA_PROJECT, then the one chosen with
simpleota project use, then the only project the token can see.

Get started:
  simpleota login                         paste a token from the dashboard
  simpleota project list                  see your projects
  simpleota project use my-fleet          choose the one to work on
  simpleota key generate --key-id prod    make a signing key
  simpleota release build/app.bin --version-label 1.0.0 --percent 10
  simpleota deployment wait               watch devices take it

Every command takes --json for a stable machine-readable result. Exit
codes: 0 OK, 1 error, 2 usage, 3 not logged in or token rejected,
4 deployment failed or stopped, 5 timed out.`,
		Version:       version.Current(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitError{code: ExitUsage, err: err}
	})
	pf := root.PersistentFlags()
	pf.BoolVar(&a.flags.json, "json", false, "print machine-readable JSON (schema in docs/json.md)")
	pf.StringVar(&a.flags.apiURL, "api-url", "", "SimpleOTA API URL (default "+config.DefaultAPIURL+", or SIMPLEOTA_API_URL)")
	pf.StringVar(&a.flags.project, "project", "", "project slug or ID to act on (default: the current project)")

	root.AddGroup(
		&cobra.Group{ID: groupCLI, Title: "Account and CLI:"},
		&cobra.Group{ID: groupProject, Title: "Projects:"},
		&cobra.Group{ID: groupResources, Title: "In the current project:"},
	)
	root.SetHelpCommandGroupID(groupCLI)
	for _, c := range []*cobra.Command{a.loginCommand(), a.logoutCommand(), a.statusCommand(), a.updateCommand()} {
		c.GroupID = groupCLI
		root.AddCommand(c)
	}
	project := a.projectCommand()
	project.GroupID = groupProject
	root.AddCommand(project)
	for _, c := range []*cobra.Command{
		a.releaseCommand(), a.buildCommand(), a.deploymentCommand(), a.deviceCommand(),
		a.channelCommand(), a.groupCommand(), a.keyCommand(), a.auditCommand(),
	} {
		c.GroupID = groupResources
		root.AddCommand(c)
	}
	return root
}
