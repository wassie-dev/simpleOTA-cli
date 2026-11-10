// Package output renders command results as human text or as the stable
// --json schema documented in docs/json.md.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

// Printer writes results to Out and notices to Err.
type Printer struct {
	Out   io.Writer
	Err   io.Writer
	JSON  bool
	Color bool
}

// WriteJSON writes v as indented JSON followed by a newline. C1 control
// characters are escaped as well as C0, so the encoded output itself holds
// no raw control characters. A tool that decodes the strings, such as
// jq -r, gets the exact values back, control characters included.
func (p Printer) WriteJSON(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := p.Out.Write(escapeC1(buf.Bytes()))
	return err
}

// escapeC1 replaces U+0080 to U+009F in encoded JSON with \u escapes. They
// can only occur inside strings, so the JSON keeps its meaning.
func escapeC1(data []byte) []byte {
	if !bytes.Contains(data, []byte{0xc2}) {
		return data
	}
	var out bytes.Buffer
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		if r >= 0x80 && r <= 0x9f {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.Write(data[:n])
		}
		data = data[n:]
	}
	return out.Bytes()
}

// Notice writes a line to Err. Notices never go to Out, so --json output
// stays parseable.
func (p Printer) Notice(format string, args ...any) {
	fmt.Fprintf(p.Err, format+"\n", args...)
}

// ErrorView is printed for a failed command in --json mode.
type ErrorView struct {
	Error    string `json:"error"`
	ExitCode int    `json:"exit_code"`
}

// table writes rows under a header as aligned columns. An empty cell
// prints as "-". Cells carry no colour, which would break the alignment.
func (p Printer) table(header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = termtext.Clean(c)
			if c == "" {
				cells[i] = "-"
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// field prints one indented "label  value" line, skipping empty values.
func (p Printer) field(label, value string) {
	p.paintedField(label, value, func(s string) string { return s })
}

// paintedField is field with the value in a colour.
func (p Printer) paintedField(label, value string, paint func(string) string) {
	if value == "" {
		return
	}
	fmt.Fprintf(p.Out, "  %-12s %s\n", label, paint(termtext.Clean(value)))
}

// nextStep prints a suggested command after a blank line, on its own line so
// it can be copied whole.
func (p Printer) nextStep(lead, command string) {
	fmt.Fprintf(p.Out, "\n%s:\n  %s\n", lead, command)
}

// indentRest indents every line after the first, so a message of several
// lines stays aligned under a field's value.
func indentRest(s string, columns int) string {
	return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", columns))
}

// when formats a time for text output, in UTC and labelled as such so output
// does not depend on the machine's zone.
func when(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// whenPtr is when for an optional time.
func whenPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return when(*t)
}

func intPtr(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

func (p Printer) paint(code, s string) string {
	if !p.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p Printer) green(s string) string { return p.paint("32", s) }

func (p Printer) red(s string) string { return p.paint("31", s) }

func (p Printer) yellow(s string) string { return p.paint("33", s) }

func (p Printer) bold(s string) string { return p.paint("1", s) }
