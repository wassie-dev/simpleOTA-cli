package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) {
	restore := SetWorkFactorForTesting(10)
	code := m.Run()
	restore()
	os.Exit(code)
}

func pass(p string) PassphraseFunc { return func() (string, error) { return p, nil } }

func TestEncryptDecryptRoundTrip(t *testing.T) {
	priv, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := Encrypt(priv, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(sealed) {
		t.Fatal("output should be an armored age file")
	}
	if strings.Contains(string(sealed), "PRIVATE KEY") {
		t.Fatal("encrypted file must not contain the PEM")
	}
	got, err := ParsePrivateKey(sealed, pass("correct horse battery"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(priv) {
		t.Fatal("decrypted key differs")
	}
}

func TestParsePrivateKey(t *testing.T) {
	priv, _ := Generate()
	sealed, _ := Encrypt(priv, "correct horse battery")
	plain, _ := PrivateKeyPEM(priv)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 1024)
	rsaDER, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	rsaPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER})

	tests := []struct {
		name    string
		data    []byte
		pass    PassphraseFunc
		wantErr string
	}{
		{name: "plain pem", data: plain},
		{name: "plain pem with whitespace", data: append([]byte("\n  "), plain...)},
		{name: "encrypted", data: sealed, pass: pass("correct horse battery")},
		{name: "wrong passphrase", data: sealed, pass: pass("wrong passphrase!!"), wantErr: "wrong passphrase"},
		{name: "no passphrase source", data: sealed, wantErr: "needs its passphrase"},
		{name: "rsa", data: rsaPEM, wantErr: "not Ed25519"},
		{name: "garbage", data: []byte("hello"), wantErr: "not a signing key"},
		{name: "public key", data: []byte(mustPublicPEM(t, priv)), wantErr: "not a signing key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePrivateKey(tt.data, tt.pass)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(priv) {
				t.Fatal("wrong key")
			}
		})
	}
}

func TestNoPassphraseIsErrPassphraseRequired(t *testing.T) {
	priv, _ := Generate()
	sealed, _ := Encrypt(priv, "correct horse battery")
	if _, err := ParsePrivateKey(sealed, nil); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("err = %v", err)
	}
}

func mustPublicPEM(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	p, err := PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublicKeyPEMRoundTrip(t *testing.T) {
	priv, _ := Generate()
	pemText := mustPublicPEM(t, priv)
	if !strings.HasPrefix(pemText, "-----BEGIN PUBLIC KEY-----\n") {
		t.Fatalf("unexpected PEM: %q", pemText)
	}
	pub, err := ParsePublicKeyPEM(pemText)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(priv.Public()) {
		t.Fatal("round trip mismatch")
	}
}

func TestSignVerifies(t *testing.T) {
	priv, _ := Generate()
	fw := []byte{0xe9, 1, 2, 3}
	sig, err := base64.StdEncoding.DecodeString(Sign(priv, fw))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), fw, sig) {
		t.Fatal("signature does not verify")
	}
	if ed25519.Verify(priv.Public().(ed25519.PublicKey), []byte{0xe9, 1, 2, 4}, sig) {
		t.Fatal("signature verifies over different bytes")
	}
}

func TestEncryptRejectsShortPassphrase(t *testing.T) {
	priv, _ := Generate()
	if _, err := Encrypt(priv, "short"); err == nil {
		t.Fatal("want error")
	}
}

func TestKeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "k.key")
	if err := WriteNewKeyFile(path, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNewKeyFile(path, []byte("other")); err == nil {
		t.Fatal("WriteNewKeyFile must not overwrite")
	}
	if got, err := ReadKeyFile(path); err != nil || string(got) != "data" {
		t.Fatalf("ReadKeyFile = %q, %v", got, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", fi.Mode().Perm())
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKeyFile(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("ReadKeyFile with 0640 = %v, want refusal", err)
	}
}

func TestReadKeyFileMissing(t *testing.T) {
	if _, err := ReadKeyFile(filepath.Join(t.TempDir(), "nope")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestFirmwareSnippetGolden(t *testing.T) {
	pub := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAaQcAzcqEJyJmEBTOkEtM9+6wX0ET8SvA8i2ABsEcs3w=\n-----END PUBLIC KEY-----\n"
	backup := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA2SJ3i0zkvYpZq6HcAF4lJ3m7ZtpCwf6RUNBBXbOC2xU=\n-----END PUBLIC KEY-----\n"
	snippetGolden(t, "snippet.golden", FirmwareSnippet(PinnedKey{"prod-2026", pub}))
	snippetGolden(t, "snippet_two_keys.golden", FirmwareSnippet(PinnedKey{"prod-2026", pub}, PinnedKey{"backup-2026", backup}, PinnedKey{"third", pub}))
}

func snippetGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("snippet mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "prod.key")
	pub, err := NewKeyFile(path, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	data, err := ReadKeyFile(path)
	if err != nil || !IsEncrypted(data) {
		t.Fatalf("key file: %v", err)
	}
	priv, err := ParsePrivateKey(data, func() (string, error) { return "correct horse battery", nil })
	if err != nil {
		t.Fatal(err)
	}
	want, _ := PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	if pub != want {
		t.Fatal("returned public key does not match the file")
	}
	if _, err := NewKeyFile(path, "correct horse battery"); err == nil {
		t.Fatal("an existing file must not be overwritten")
	}
}
