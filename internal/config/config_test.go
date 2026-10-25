package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeAPIURL(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://simpleota.com", want: "https://simpleota.com"},
		{in: "https://simpleota.com/", want: "https://simpleota.com"},
		{in: " https://SimpleOTA.com ", want: "https://simpleota.com"},
		{in: "https://staging.example.com:8443", want: "https://staging.example.com:8443"},
		{in: "http://localhost:8000", want: "http://localhost:8000"},
		{in: "http://127.0.0.1:8765", want: "http://127.0.0.1:8765"},
		{in: "http://[::1]:8000", want: "http://[::1]:8000"},
		{in: "http://simpleota.com", wantErr: true},
		{in: "http://10.0.0.5:8000", wantErr: true},
		{in: "ftp://simpleota.com", wantErr: true},
		{in: "simpleota.com", wantErr: true},
		{in: "https://simpleota.com/api/v1", wantErr: true},
		{in: "https://user:pw@simpleota.com", wantErr: true},
		{in: "https://simpleota.com?x=1", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		got, err := NormalizeAPIURL(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("NormalizeAPIURL(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeAPIURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "simpleota")
	want := Config{APIURL: "https://simpleota.com", ProjectID: "p", ProjectSlug: "fleet", SigningKeyFile: "/k"}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("config mode = %o, want 600", fi.Mode().Perm())
		}
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil || c != (Config{}) {
		t.Fatalf("Load = %+v, %v", c, err)
	}
}

func TestLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("want error for corrupt config")
	}
}

func TestDirOverride(t *testing.T) {
	got, err := Dir(func(k string) string {
		if k == "SIMPLEOTA_CONFIG_DIR" {
			return "/tmp/x/"
		}
		return ""
	})
	if err != nil || got != filepath.Clean("/tmp/x") {
		t.Fatalf("Dir = %q, %v", got, err)
	}
}
