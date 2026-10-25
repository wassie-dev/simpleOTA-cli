// Package output renders command results as human text or as the stable
// --json schema documented in docs/json.md.
package output

import (
	"encoding/json"
	"fmt"
	"io"
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
