package output

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/output -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s mismatch:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func render(t *testing.T, json bool, f func(Printer) error) []byte {
	t.Helper()
	var out, errBuf bytes.Buffer
	if err := f(Printer{Out: &out, Err: &errBuf, JSON: json}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestErrorGolden(t *testing.T) {
	v := ErrorView{Error: "not logged in", ExitCode: 3}
	golden(t, "error.json", render(t, true, func(p Printer) error { return p.WriteJSON(v) }))
}
