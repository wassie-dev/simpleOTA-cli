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
	"github.com/wassie-dev/simpleOTA-cli/internal/version"
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

// exitError carries a specific exit code.
type exitError struct {
	code int
	err  error
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
	if strings.HasPrefix(err.Error(), "unknown command") {
		return ExitUsage
	}
	return ExitError
}

func (a *App) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "simpleota",
		Short:         "Sign, release and roll out ESP32 firmware with SimpleOTA",
		Version:       version.Version,
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
	pf.StringVar(&a.flags.project, "project", "", "project ID or slug (default: the project chosen at login)")

	root.AddCommand(
		a.loginCommand(),
		a.logoutCommand(),
		a.keysCommand(),
		a.releaseCommand(),
	)
	return root
}
