package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func usageErr(format string, args ...any) error {
	return &exitError{code: ExitUsage, err: fmt.Errorf(format, args...)}
}

func authErr(format string, args ...any) error {
	return &exitError{code: ExitAuth, err: fmt.Errorf(format, args...)}
}

// exactArgs is cobra.ExactArgs with the usage exit code.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErr("%s takes %d argument(s), got %d; see simpleota %s --help", cmd.Name(), n, len(args), cmd.Name())
		}
		return nil
	}
}

// minArgs is cobra.MinimumNArgs with the usage exit code.
func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return usageErr("%s takes at least %d argument(s), got %d; see %s --help", cmd.Name(), n, len(args), cmd.CommandPath())
		}
		return nil
	}
}

// groupRun makes a command group print its help when called bare and
// reject an unknown subcommand with the usage exit code.
func groupRun(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErr("unknown command %q for %q; see %s --help", args[0], cmd.CommandPath(), cmd.CommandPath())
	}
	return cmd.Help()
}
