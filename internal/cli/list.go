package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// defaultListLimit is how many rows a list command prints as text.
const defaultListLimit = 20

// withLimit gives a list command its --limit flag and says what it defaults
// to. printList cuts a full listing; the server pages only a few lists.
func withLimit(cmd *cobra.Command) *cobra.Command {
	cmd.Long += fmt.Sprintf("\n\nBy default this shows %d rows as text, and every row as JSON.", defaultListLimit)
	cmd.Flags().Int("limit", defaultListLimit, "show at most `n` rows, or 0 for all")
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if limit, _ := cmd.Flags().GetInt("limit"); limit < 0 {
			return usageErr("--limit must be 0 or more")
		}
		return nil
	}
	return cmd
}

// listLimit is how many rows a list command shows, or 0 for all. JSON stays
// complete unless --limit was given, because a script would not see the
// note that rows were left out and would carry on with part of the data.
func listLimit(a *App, cmd *cobra.Command) int {
	if a.flags.json && !cmd.Flags().Changed("limit") {
		return 0
	}
	limit, _ := cmd.Flags().GetInt("limit")
	return limit
}

// printList renders rows up to the --limit flag, and says when it left some
// out.
func printList[T any](a *App, cmd *cobra.Command, rows []T, render func([]T) error) error {
	shown := rows
	if limit := listLimit(a, cmd); limit > 0 && len(rows) > limit {
		shown = rows[:limit]
	}
	return printSome(a, shown, len(rows), render)
}

// printSome renders rows, and says when they are fewer than total.
func printSome[T any](a *App, rows []T, total int, render func([]T) error) error {
	if err := render(rows); err != nil {
		return err
	}
	if len(rows) < total && !a.flags.json {
		a.printer().Notice("\nShowing %d of %d. Use --limit <n> for more, or --limit 0 for all.", len(rows), total)
	}
	return nil
}
