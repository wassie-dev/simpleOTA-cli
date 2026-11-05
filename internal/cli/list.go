package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// defaultListLimit is how many rows a list command prints as text.
const defaultListLimit = 20

// withLimit gives a list command its --limit flag and says what it defaults
// to. The server has no pagination, so printList cuts the full listing.
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

// printList renders rows up to the --limit flag, and says when it left some
// out. JSON stays complete unless --limit was given, because a script would
// not see that note and would carry on with part of the data.
func printList[T any](a *App, cmd *cobra.Command, rows []T, render func([]T) error) error {
	limit, _ := cmd.Flags().GetInt("limit")
	if a.flags.json && !cmd.Flags().Changed("limit") {
		limit = 0
	}
	shown := rows
	if limit > 0 && len(rows) > limit {
		shown = rows[:limit]
	}
	if err := render(shown); err != nil {
		return err
	}
	if len(shown) < len(rows) && !a.flags.json {
		a.printer().Notice("\nShowing %d of %d. Use --limit <n> for more, or --limit 0 for all.", len(shown), len(rows))
	}
	return nil
}
