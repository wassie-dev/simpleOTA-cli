package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		token   string
		want    Kind
		wantErr bool
	}{
		{token: "soto_pat_abcdefghijklmnopqrstuvwxyz012345678901234", want: KindPersonal},
		{token: "soto_proj_abcdefghijklmnopqrstuvwxyz0123456789-_A", want: KindProject},
		{token: "soto_dev_abcdefghijklmnopqrstuvwxyz", wantErr: true},
		{token: "soto_pat_short", wantErr: true},
		{token: "soto_pat_abcdefghijklmnop qrstuvwxyz", wantErr: true},
		{token: "Bearer soto_pat_abcdefghijklmnopqrstuvwxyz", wantErr: true},
		{token: "", wantErr: true},
	}
	for _, tt := range tests {
		got, err := Classify(tt.token)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("Classify(%q) = %q, %v; want %q, wantErr %v", tt.token, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestClassifyErrorDoesNotEchoToken(t *testing.T) {
	secret := "soto_pat_" + strings.Repeat("x", 5)
	_, err := Classify(secret)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error %v must not contain the token", err)
	}
}

func TestFileStore(t *testing.T) {
	f := File{Path: filepath.Join(t.TempDir(), "sub", "credentials.json")}
	if _, err := f.Get("https://a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty store = %v, want ErrNotFound", err)
	}
	if err := f.Set("https://a", "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("http://127.0.0.1:8765", "tok-b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get("https://a"); got != "tok-a" {
		t.Fatalf("Get = %q", got)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(f.Path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 600", fi.Mode().Perm())
		}
	}
	if err := f.Delete("https://a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("https://a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v", err)
	}
	if err := f.Delete("http://127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
		t.Fatalf("file should be removed when empty, stat err = %v", err)
	}
}

func TestFileStoreRefusesLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"https://a":"t"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (File{Path: path}).Get("https://a"); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("Get = %v, want permissions error", err)
	}
}

type memStore struct {
	name   string
	tokens map[string]string
	broken bool
}

func (m *memStore) Name() string { return m.name }
func (m *memStore) Get(u string) (string, error) {
	if m.broken {
		return "", errors.New("unavailable")
	}
	t, ok := m.tokens[u]
	if !ok {
		return "", ErrNotFound
	}
	return t, nil
}
func (m *memStore) Set(u, t string) error {
	if m.broken {
		return errors.New("unavailable")
	}
	m.tokens[u] = t
	return nil
}
func (m *memStore) Delete(u string) error {
	if m.broken {
		return errors.New("unavailable")
	}
	delete(m.tokens, u)
	return nil
}

func TestFallbackUsesSecondaryWhenPrimaryUnavailable(t *testing.T) {
	primary := &memStore{name: "keychain", tokens: map[string]string{}, broken: true}
	secondary := &memStore{name: "file", tokens: map[string]string{}}
	fb := Fallback{Primary: primary, Secondary: secondary}
	where, err := fb.SetWhere("u", "t")
	if err != nil || where != "file" {
		t.Fatalf("SetWhere = %q, %v", where, err)
	}
	if got, err := fb.Get("u"); err != nil || got != "t" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := fb.Delete("u"); err != nil {
		t.Fatal(err)
	}
	if _, err := fb.Get("u"); err == nil {
		t.Fatal("Get after Delete should fail")
	}
}

func TestFallbackPrefersPrimaryAndClearsSecondary(t *testing.T) {
	primary := &memStore{name: "keychain", tokens: map[string]string{}}
	secondary := &memStore{name: "file", tokens: map[string]string{"u": "old"}}
	fb := Fallback{Primary: primary, Secondary: secondary}
	where, err := fb.SetWhere("u", "new")
	if err != nil || where != "keychain" {
		t.Fatalf("SetWhere = %q, %v", where, err)
	}
	if _, ok := secondary.tokens["u"]; ok {
		t.Fatal("stale file copy should be removed")
	}
	if got, _ := fb.Get("u"); got != "new" {
		t.Fatalf("Get = %q", got)
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"soto_pat_secretsecret":  "soto_pat_…",
		"soto_proj_secretsecret": "soto_proj_…",
		"whatever":               "…",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFallbackReportsKeychainErrorWhenFileIsEmpty(t *testing.T) {
	fb := Fallback{
		Primary:   &memStore{name: "the OS keychain", tokens: map[string]string{}, broken: true},
		Secondary: &memStore{name: "file", tokens: map[string]string{}},
	}
	_, err := fb.Get("u")
	if err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("err = %v, want the keychain failure", err)
	}
}
