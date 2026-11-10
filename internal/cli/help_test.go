package cli

import (
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

// allCommands walks the command tree, leaving out cobra's hidden help command.
func allCommands(c *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{c}
	for _, sub := range c.Commands() {
		if !sub.Hidden {
			out = append(out, allCommands(sub)...)
		}
	}
	return out
}

// bareCommand finds a command written in running text without quotes.
var bareCommand = regexp.MustCompile(`(^|[^'\w-])simpleota (login|logout|status|update|release|project|build|deployment|device|channel|group|key|audit)\b`)

// TestHelpStyle enforces the conventions in help.go.
func TestHelpStyle(t *testing.T) {
	h := newHarness(t)
	root := (&App{Getenv: func(k string) string { return h.env[k] }}).rootCommand()
	for _, c := range allCommands(root) {
		path := c.CommandPath()
		if c.Short == "" || len(c.Short) > 56 || !unicode.IsUpper(rune(c.Short[0])) || strings.HasSuffix(c.Short, ".") {
			t.Errorf("%s: Short %q should be a capitalised phrase of at most 56 characters with no period", path, c.Short)
		}
		if c.Long == "" {
			t.Errorf("%s: no Long", path)
		}
		for _, line := range strings.Split(c.Long, "\n") {
			if len(line) > 78 {
				t.Errorf("%s: Long line is over 78 columns: %q", path, line)
			}
			if !strings.HasPrefix(line, " ") && bareCommand.MatchString(line) {
				t.Errorf("%s: a command in running text needs single quotes: %q", path, line)
			}
		}
		if c.Name() == "list" && c.Flags().Lookup("limit") == nil {
			t.Errorf("%s: a list command needs --limit; wrap it in withLimit", path)
		}
		if c != root && c.Example == "" {
			t.Errorf("%s: no Example", path)
		}
		for _, line := range strings.Split(c.Example, "\n") {
			if line != "" && !strings.HasPrefix(line, "  ") {
				t.Errorf("%s: Example line is not indented: %q", path, line)
			}
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			name, usage := pflag.UnquoteUsage(f)
			if name == "string" || name == "stringArray" || name == "int" {
				t.Errorf("%s --%s: give the flag a placeholder, not %q", path, f.Name, name)
			}
			if strings.HasSuffix(usage, ".") || unicode.IsUpper(rune(usage[0])) {
				t.Errorf("%s --%s: usage %q should be lower case with no period", path, f.Name, usage)
			}
		})
	}
}

// TestHelpFitsEightyColumns renders every help page.
func TestHelpFitsEightyColumns(t *testing.T) {
	h := newHarness(t)
	root := (&App{Getenv: func(k string) string { return h.env[k] }}).rootCommand()
	for _, c := range allCommands(root) {
		args := append(strings.Fields(strings.TrimPrefix(c.CommandPath(), "simpleota")), "--help")
		r := h.run(args...)
		if r.code != 0 || !strings.Contains(r.stdout, "Usage:") {
			t.Errorf("%s: %+v", c.CommandPath(), r)
		}
		if strings.Contains(r.stdout, "help for ") || strings.Contains(r.stdout, "stringArray") || strings.Contains(r.stdout, "Aliases:") {
			t.Errorf("%s: cobra boilerplate in help:\n%s", c.CommandPath(), r.stdout)
		}
		for _, line := range strings.Split(r.stdout, "\n") {
			if len(line) > 80 {
				t.Errorf("%s: line over 80 columns: %q", c.CommandPath(), line)
			}
		}
	}
}

func TestUsageErrorsNameTheRightCommand(t *testing.T) {
	h := newHarness(t)
	r := h.run("project", "use")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "'simpleota project use' takes 1 argument, but got 0") || !strings.Contains(r.stderr, "simpleota project use <slug|id>") {
		t.Fatalf("too few: %+v", r)
	}
	r = h.run("status", "extra", "more")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "takes no arguments, but got 2") {
		t.Fatalf("too many: %+v", r)
	}
	r = h.run("group", "member", "add", "x")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "takes at least 2 arguments, but got 1") {
		t.Fatalf("too few for a nested command: %+v", r)
	}
	r = h.run("deployment", "lst")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "Did you mean 'simpleota deployment list'?") || !strings.Contains(r.stderr, "See 'simpleota deployment --help'.") {
		t.Fatalf("unknown subcommand: %+v", r)
	}
	r = h.run("nope")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "See 'simpleota --help'.") {
		t.Fatalf("unknown command: %+v", r)
	}
	r = h.run("status", "--nope")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "See 'simpleota status --help'.") {
		t.Fatalf("unknown flag: %+v", r)
	}
	if r := h.run("-h"); r.code != 0 || !strings.Contains(r.stdout, "Usage:") {
		t.Fatalf("-h: %+v", r)
	}
	if r := h.run("help", "login"); r.code != 0 || !strings.Contains(r.stdout, "simpleota login") {
		t.Fatalf("help command: %+v", r)
	}
	if r := h.run("--version"); r.code != 0 || !strings.Contains(r.stdout, "version") {
		t.Fatalf("--version: %+v", r)
	}
}

// A command the reader should run sits on its own line, even inside an error.
func TestSuggestedCommandsAreOnTheirOwnLine(t *testing.T) {
	h := newHarness(t)
	r := h.run("deployment", "list")
	if !strings.Contains(r.stderr, "Log in with:\n  simpleota login\n") {
		t.Fatalf("not logged in: %+v", r)
	}
	h.login(fakeapi.ProjectToken)
	h.fake.AddDeployment(depA, api.StateActive, 5, 100)
	r = h.run("deployment", "pause", depA)
	if !strings.Contains(r.stdout, "\n\nTo ship a fix") || !strings.Contains(r.stdout, ":\n  simpleota release <bin> --version-label <label>\n") {
		t.Fatalf("next step: %+v", r)
	}
}
