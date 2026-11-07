package termtext

import "testing"

func TestClean(t *testing.T) {
	tests := map[string]string{
		"lamp-1":              "lamp-1",
		"caf\xc3\xa9":         "caf\xc3\xa9",
		"\x1b[31mRED\x1b[0m":  `\x1b[31mRED\x1b[0m`,
		"\x1b]0;title\x07":    `\x1b]0;title\x07`,
		"ok\rFAKE\bX\tY":      `ok\x0dFAKE\x08X\x09Y`,
		"\xc2\x9b31m":         `\x9b31m`,
		"line\n2nd-line":      `line\x0a2nd-line`,
		"abc\xe2\x80\xaedcba": "abc" + `\` + "u202edcba",
		"bad\xffbyte":         "bad\xef\xbf\xbdbyte",
		"del\x7f":             `del\x7f`,
	}
	for in, want := range tests {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CleanLines("one\ntwo\x1b[2J"); got != "one\n"+`two\x1b[2J` {
		t.Errorf("CleanLines: %q", got)
	}
}
