package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/update"
	"github.com/wassie-dev/simpleOTA-cli/internal/version"
)

func (a *App) updateCommand() *cobra.Command {
	var check bool
	var want string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update simpleota to the latest release",
		Long: `Download the latest simpleota release from GitHub, check it against the
release's checksums.txt, and replace this binary with it. --check only
reports whether a newer release exists. --version installs a specific
release, older ones included.

If the binary's folder is not writable, rerun with sudo or reinstall with
the install script.`,
		Example: `  simpleota update
  simpleota update --check
  simpleota update --version 0.1.0`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runUpdate(cmd.Context(), check, want)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether an update is available")
	cmd.Flags().StringVar(&want, "version", "", "install this version instead of the latest")
	return cmd
}

func (a *App) currentVersion() string {
	if a.CurrentVersion != nil {
		return a.CurrentVersion()
	}
	return version.Current()
}

func (a *App) updateSource() (update.Source, error) {
	if a.UpdateSource != nil {
		return *a.UpdateSource, nil
	}
	return update.NewSource(a.Getenv("SIMPLEOTA_DOWNLOAD_URL"))
}

func (a *App) executable() (string, error) {
	exe := os.Executable
	if a.Executable != nil {
		exe = a.Executable
	}
	p, err := exe()
	if err != nil {
		return "", fmt.Errorf("finding the simpleota binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p, nil
}

func (a *App) runUpdate(ctx context.Context, check bool, want string) error {
	current := a.currentVersion()
	src, err := a.updateSource()
	if err != nil {
		return usageErr("%v", err)
	}

	target := ""
	if want != "" {
		target = update.Normalize(want)
		if target == "" {
			return usageErr("invalid --version %q: use a release version such as 0.1.0", want)
		}
	} else {
		if target, err = src.Latest(ctx); err != nil {
			return err
		}
	}

	view := output.UpdateView{
		Current:         current,
		Latest:          strings.TrimPrefix(target, "v"),
		UpdateAvailable: update.Normalize(current) == "" || update.Newer(target, current),
	}
	p := a.printer()

	if check {
		if p.JSON {
			return p.WriteJSON(view)
		}
		switch {
		case update.Normalize(current) == "":
			fmt.Fprintf(a.Out, "This is a development build; the latest release is %s.\n", view.Latest)
		case view.UpdateAvailable:
			fmt.Fprintf(a.Out, "simpleota %s is available (you have %s). Run: simpleota update\n", view.Latest, current)
		default:
			fmt.Fprintf(a.Out, "simpleota %s is up to date.\n", current)
		}
		return nil
	}

	if update.Normalize(current) == "" && want == "" {
		return usageErr("this is a development build (%s); pass --version to install a release over it", current)
	}
	if want == "" && !view.UpdateAvailable {
		if p.JSON {
			return p.WriteJSON(view)
		}
		fmt.Fprintf(a.Out, "simpleota %s is up to date.\n", current)
		return nil
	}
	if want != "" && update.Normalize(current) == target {
		if p.JSON {
			return p.WriteJSON(view)
		}
		fmt.Fprintf(a.Out, "simpleota %s is already installed.\n", current)
		return nil
	}

	exe, err := a.executable()
	if err != nil {
		return err
	}
	update.RemoveLeftover(exe)
	if !p.JSON {
		p.Notice("Downloading simpleota %s", view.Latest)
	}
	binary, err := src.Download(ctx, target)
	if err != nil {
		return err
	}
	if err := update.Replace(exe, binary); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cannot replace %s: permission denied; rerun with sudo, or reinstall with the install script", exe)
		}
		return fmt.Errorf("replacing %s: %w", exe, err)
	}

	view.Updated = true
	view.Path = exe
	if p.JSON {
		return p.WriteJSON(view)
	}
	fmt.Fprintf(a.Out, "Updated simpleota from %s to %s at %s.\n", current, view.Latest, exe)
	return nil
}
