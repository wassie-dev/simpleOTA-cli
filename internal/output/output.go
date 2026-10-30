// Package output renders command results as human text or as the stable
// --json schema documented in docs/json.md.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Printer writes results to Out and notices to Err.
type Printer struct {
	Out   io.Writer
	Err   io.Writer
	JSON  bool
	Color bool
}

// WriteJSON writes v as indented JSON followed by a newline.
func (p Printer) WriteJSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
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
			cells[i] = c
			if c == "" {
				cells[i] = "-"
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// when formats a time for a table, in UTC so output does not depend on the
// machine's zone.
func when(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// whenPtr is when for an optional time.
func whenPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return when(*t)
}
