// Package termtext makes text from the server safe to print to a terminal.
package termtext

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Clean shows each control character in s as an escape such as \x1b, so
// that text from the server cannot move the cursor, recolour or retitle the
// terminal, reorder text, or start a line of its own. A backslash is doubled,
// so text cannot pass itself off as one of these escapes. Invalid UTF-8
// becomes U+FFFD.
func Clean(s string) string {
	return clean(s, false)
}

// CleanLines is Clean for the CLI's own messages of several lines: it keeps
// newlines.
func CleanLines(s string) string {
	return clean(s, true)
}

func clean(s string, keepNewlines bool) string {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return needsEscape(r, keepNewlines) }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case !needsEscape(r, keepNewlines):
			b.WriteRune(r)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// needsEscape reports the characters Clean escapes. CleanLines keeps
// newlines and backslashes, since it is for the CLI's own messages, in which
// server text is already cleaned.
func needsEscape(r rune, keepNewlines bool) bool {
	if r == '\n' || r == '\\' {
		return !keepNewlines
	}
	return unicode.IsControl(r) || isBidiControl(r)
}

// isBidiControl reports the Unicode bidirectional formatting characters,
// which can make text display in a different order than it is stored.
func isBidiControl(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}
