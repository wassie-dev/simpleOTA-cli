package cli

import (
	"github.com/spf13/cobra"
)

// Conventions for all text the CLI prints:
//
//   - A command the reader should run goes on its own line, indented two
//     spaces, after a lead-in ending in a colon, so it stands out and can be
//     copied whole.
//   - A command mentioned in a sentence goes in single quotes, in full:
//     'simpleota project use'. Never use a bare command name as a word.
//   - The first line of an error says what is wrong, with no period. Any
//     lines after it say what to do, as full sentences.
//   - Help starts with one sentence saying what the command does, prefers
//     short lists to long paragraphs, and fits 80 columns.
//
// TestHelpStyle checks what it can of this.

// helpWidth is the column flag descriptions wrap at.
const helpWidth = "80"

// usageTemplate lists sections from most to least needed, with examples last
// so they sit just above the prompt. It leaves out aliases and shows a group
// of commands as "<command>" rather than as something runnable with flags.
const usageTemplate = `Usage:{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <command>{{else}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Commands:{{range $cmds}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) .IsAvailableCommand)}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsagesWrapped ` + helpWidth + ` | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global flags:
{{.InheritedFlags.FlagUsagesWrapped ` + helpWidth + ` | trimTrailingWhitespaces}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Run '{{.CommandPath}} <command> --help' for details.{{end}}
`

const rootLong = `simpleota signs, uploads and rolls out ESP32 firmware with SimpleOTA.

Get started:
  simpleota login                                paste a dashboard token
  simpleota project use my-fleet                 choose a project
  simpleota key generate --key-id prod           make a signing key
  simpleota release app.bin --version-label 1.0  upload and deploy a build
  simpleota deployment show                      follow its device progress

Most commands act on the current project; see 'simpleota project --help'.
Add --json to any command for machine-readable output.

Exit codes: 0 success, 1 error, 2 bad usage, 3 not logged in or token
rejected, 4 no active device in the deployment's audience has compatible
metadata (with --require-devices).`

// setupHelp applies the help layout to the root command, once every command
// has been added. Commands stay in the order they were added, which follows
// the workflow, and the --help flag and help command work without being
// listed on every page.
func setupHelp(root *cobra.Command) {
	cobra.EnableCommandSorting = false
	root.SetUsageTemplate(usageTemplate)
	root.PersistentFlags().BoolP("help", "h", false, "show help")
	_ = root.PersistentFlags().MarkHidden("help")
	root.Flags().BoolP("version", "v", false, "print the version")
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			c.Hidden = true
		}
	}
}
