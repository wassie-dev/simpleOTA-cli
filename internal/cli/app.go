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
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/browser"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
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
	// OpenBrowser opens a URL in the user's browser; nil means this session
	// cannot show one.
	OpenBrowser func(url string) error
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
		if browser.Available(runtime.GOOS, os.Getenv) {
			a.OpenBrowser = browser.Open
		}
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
		fmt.Fprintln(a.Err, "Error: "+termtext.CleanLines(msg))
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
			return "SimpleOTA rejected the token (401); it may be revoked or mistyped\nLog in again with:\n  simpleota login\nOr check " + tokenEnv + "."
		case http.StatusForbidden:
			return "SimpleOTA refused this for your token (403)\nA project token reaches only its own project, and some actions need a\npersonal token."
		}
	}
	msg := err.Error()
	if isUnknownCommand(err) && !strings.Contains(msg, "--help") {
		return strings.TrimSpace(msg) + "\nSee 'simpleota --help'."
	}
	return msg
}

// isUnknownCommand recognises cobra's plain error for an unknown top-level
// command.
func isUnknownCommand(err error) bool {
	return strings.HasPrefix(err.Error(), "unknown command")
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
	if isUnknownCommand(err) {
		return ExitUsage
	}
	return ExitError
}

func (a *App) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "simpleota",
		Short:         "Sign, release and roll out ESP32 firmware with SimpleOTA",
		Long:          rootLong,
		Version:       version.Current(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageErr("%v\nSee '%s --help'.", err, cmd.CommandPath())
	})
	pf := root.PersistentFlags()
	pf.BoolVar(&a.flags.json, "json", false, "print JSON instead of text")
	pf.StringVar(&a.flags.apiURL, "api-url", "", "server `url` (default simpleota.com, or SIMPLEOTA_API_URL)")
	pf.StringVar(&a.flags.project, "project", "", "project to act on, by `slug` or ID")

	root.AddGroup(
		&cobra.Group{ID: groupCLI, Title: "Account and CLI:"},
		&cobra.Group{ID: groupProject, Title: "Projects:"},
		&cobra.Group{ID: groupResources, Title: "In the current project:"},
	)
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
	setupHelp(root)
	return root
}
