package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// viewsOf converts each item for output.
func viewsOf[T, V any](items []T, view func(T) V) []V {
	views := make([]V, len(items))
	for i, item := range items {
		views[i] = view(item)
	}
	return views
}

func usageErr(format string, args ...any) error {
	return &exitError{code: ExitUsage, err: fmt.Errorf(format, args...)}
}

func authErr(format string, args ...any) error {
	return &exitError{code: ExitAuth, err: fmt.Errorf(format, args...)}
}

// argCountErr says how many arguments a command takes and shows its usage.
func argCountErr(cmd *cobra.Command, want string, got int) error {
	return usageErr("'%s' takes %s, but got %d\nUsage:\n  %s", cmd.CommandPath(), want, got, cmd.UseLine())
}

// argCount renders a number of arguments.
func argCount(n int) string {
	switch n {
	case 0:
		return "no arguments"
	case 1:
		return "1 argument"
	}
	return fmt.Sprintf("%d arguments", n)
}

// exactArgs is cobra.ExactArgs with the usage exit code.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return argCountErr(cmd, argCount(n), len(args))
		}
		return nil
	}
}

func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return argCountErr(cmd, "at most "+argCount(n), len(args))
		}
		return nil
	}
}

// minArgs is cobra.MinimumNArgs with the usage exit code.
func minArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < n {
			return argCountErr(cmd, "at least "+argCount(n), len(args))
		}
		return nil
	}
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// groupRun makes a command group print its help when called bare and
// reject an unknown subcommand with the usage exit code.
func groupRun(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		hint := ""
		cmd.SuggestionsMinimumDistance = 2
		if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
			hint = fmt.Sprintf("\nDid you mean '%s %s'?", cmd.CommandPath(), s[0])
		}
		return usageErr("unknown command %q for '%s'%s\nSee '%s --help'.", args[0], cmd.CommandPath(), hint, cmd.CommandPath())
	}
	return cmd.Help()
}
