package update_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wassie-dev/simpleOTA-cli/internal/fakerelease"
	"github.com/wassie-dev/simpleOTA-cli/internal/update"
)

var newBinary = []byte("#!/bin/sh\necho new\n")

func TestNormalizeAndNewer(t *testing.T) {
	for in, want := range map[string]string{
		"0.1.0": "v0.1.0", "v1.2.3": "v1.2.3", " 1.0.0 ": "v1.0.0",
		"0.0.0-SNAPSHOT-16b04ed":                   "v0.0.0-SNAPSHOT-16b04ed",
		"v0.0.0-20261025161709-0f6c0037fb97+dirty": "v0.0.0-20261025161709-0f6c0037fb97+dirty",
		"dev": "", "": "", "1.0": "", "latest": "",
	} {
		if got := update.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	tests := []struct {
		candidate, current string
		want               bool
	}{
		{"0.2.0", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.1.0", "v0.0.0-20261025161709-0f6c0037fb97", true},
		{"1.0.0", "1.0.0-rc.1", true},
	}
	for _, tt := range tests {
		if got := update.Newer(tt.candidate, tt.current); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.candidate, tt.current, got)
		}
	}
}

func TestLatest(t *testing.T) {
	rel := fakerelease.New(t, "1.2.0", "linux", "amd64", newBinary)
	v, err := rel.Source().Latest(context.Background())
	if err != nil || v != "v1.2.0" {
		t.Fatalf("Latest = %q, %v", v, err)
	}
	rel.Set(func(s *fakerelease.Server) { s.NoRelease = true })
	if _, err := rel.Source().Latest(context.Background()); !errors.Is(err, update.ErrNoRelease) {
		t.Fatalf("err = %v, want ErrNoRelease", err)
	}
}

func TestDownloadVerifiesAndExtracts(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			rel := fakerelease.New(t, "1.2.0", goos, "arm64", newBinary)
			got, err := rel.Source().Download(context.Background(), "1.2.0")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, newBinary) {
				t.Fatalf("binary = %q", got)
			}
		})
	}
}

func TestDownloadRefusesChecksumMismatch(t *testing.T) {
	rel := fakerelease.New(t, "1.2.0", "linux", "amd64", newBinary)
	rel.Set(func(s *fakerelease.Server) { s.BadChecksum = true })
	_, err := rel.Source().Download(context.Background(), "1.2.0")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadUnknownVersion(t *testing.T) {
	rel := fakerelease.New(t, "1.2.0", "linux", "amd64", newBinary)
	_, err := rel.Source().Download(context.Background(), "9.9.9")
	if err == nil || !strings.Contains(err.Error(), "has no checksums.txt") {
		t.Fatalf("err = %v", err)
	}
	if rel.Downloads != 0 {
		t.Fatal("no archive should be downloaded")
	}
}

func TestNewSourceMirrorRules(t *testing.T) {
	for _, ok := range []string{"", "https://mirror.example.com/simpleota", "http://127.0.0.1:8766", "http://localhost:9000"} {
		if _, err := update.NewSource(ok); err != nil {
			t.Errorf("NewSource(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"http://mirror.example.com", "ftp://x", "not a url"} {
		if _, err := update.NewSource(bad); err == nil {
			t.Errorf("NewSource(%q) should fail", bad)
		}
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "simpleota")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := update.Replace(exe, newBinary); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if !bytes.Equal(got, newBinary) {
		t.Fatalf("content = %q", got)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(exe)
		if fi.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %o", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".simpleota-update-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestReplaceUnwritableDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "simpleota")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := update.Replace(exe, newBinary); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want permission error", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old" {
		t.Fatal("the old binary must be untouched")
	}
}
