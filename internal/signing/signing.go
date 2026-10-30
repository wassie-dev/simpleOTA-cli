// Package signing generates, protects, loads and uses Ed25519 firmware
// signing keys. Private keys never leave the user's machine: only public
// keys and signatures are sent to SimpleOTA.
package signing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"filippo.io/age"
	"filippo.io/age/armor"

	"github.com/wassie-dev/simpleOTA-cli/internal/fileperm"
)

// MinPassphraseLen is the shortest passphrase accepted for a new key file.
const MinPassphraseLen = 12

// scryptWorkFactor is age's default (about one second); tests lower it.
var scryptWorkFactor = 18

const (
	pemPrivateType = "PRIVATE KEY"
	pemPublicType  = "PUBLIC KEY"
	maxKeyFileSize = 64 << 10
)

// ErrPassphraseRequired means the key is encrypted and no passphrase source
// is available.
var ErrPassphraseRequired = errors.New("the signing key is encrypted and needs its passphrase")

// Generate creates a new Ed25519 keypair from crypto/rand.
func Generate() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	return priv, nil
}

// PublicKeyPEM returns the SubjectPublicKeyInfo PEM the server and the
// firmware clients expect.
func PublicKeyPEM(pub ed25519.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: pemPublicType, Bytes: der})), nil
}

// ParsePublicKeyPEM parses an Ed25519 SubjectPublicKeyInfo PEM.
func ParsePublicKeyPEM(s string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil || block.Type != pemPublicType {
		return nil, errors.New("not a PEM public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an Ed25519 public key")
	}
	return pub, nil
}

// PrivateKeyPEM returns the unencrypted PKCS#8 PEM, the format OpenSSL and
// the SimpleOTA GitHub Action read.
func PrivateKeyPEM(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemPrivateType, Bytes: der}), nil
}

// CheckPassphrase enforces the minimum strength for a new key's passphrase.
func CheckPassphrase(p string) error {
	if utf8.RuneCountInString(p) < MinPassphraseLen {
		return fmt.Errorf("the passphrase must be at least %d characters", MinPassphraseLen)
	}
	return nil
}

// Encrypt seals the private key as an ASCII-armored age file protected by
// the passphrase (scrypt).
func Encrypt(priv ed25519.PrivateKey, passphrase string) ([]byte, error) {
	if err := CheckPassphrase(passphrase); err != nil {
		return nil, err
	}
	plain, err := PrivateKeyPEM(priv)
	if err != nil {
		return nil, err
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	r.SetWorkFactor(scryptWorkFactor)

	var out bytes.Buffer
	aw := armor.NewWriter(&out)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// IsEncrypted reports whether data is an armored age file.
func IsEncrypted(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(data), []byte(armor.Header))
}

// PassphraseFunc supplies the passphrase for an encrypted key when needed.
type PassphraseFunc func() (string, error)

// ParsePrivateKey accepts an armored age file or an unencrypted PKCS#8 PEM.
func ParsePrivateKey(data []byte, passphrase PassphraseFunc) (ed25519.PrivateKey, error) {
	data = bytes.TrimSpace(data)
	if IsEncrypted(data) {
		if passphrase == nil {
			return nil, ErrPassphraseRequired
		}
		p, err := passphrase()
		if err != nil {
			return nil, err
		}
		id, err := age.NewScryptIdentity(p)
		if err != nil {
			return nil, err
		}
		r, err := age.Decrypt(armor.NewReader(bytes.NewReader(data)), id)
		if err != nil {
			var noMatch *age.NoIdentityMatchError
			if errors.As(err, &noMatch) {
				return nil, errors.New("wrong passphrase for the signing key")
			}
			return nil, fmt.Errorf("decrypting the signing key: %w", err)
		}
		plain, err := io.ReadAll(io.LimitReader(r, maxKeyFileSize))
		if err != nil {
			return nil, fmt.Errorf("decrypting the signing key: %w", err)
		}
		data = plain
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != pemPrivateType {
		return nil, errors.New("not a signing key: expected an encrypted simpleota key file or a PEM \"BEGIN PRIVATE KEY\" block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the private key: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("the private key is not Ed25519; SimpleOTA signs firmware with Ed25519 only")
	}
	return priv, nil
}

// ReadKeyFile reads a key file after checking it is private to the owner.
func ReadKeyFile(path string) ([]byte, error) {
	if err := fileperm.CheckPrivate(path); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("signing key file %s does not exist", path)
		}
		return nil, fmt.Errorf("refusing to read the signing key: %w", err)
	}
	f, err := os.Open(path) // #nosec G304 -- the user names their own key file
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxKeyFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxKeyFileSize {
		return nil, fmt.Errorf("%s is too large to be a signing key", path)
	}
	return data, nil
}

// NewKeyFile generates an Ed25519 key, writes it to path encrypted with the
// passphrase (0600, failing if path exists), and returns its public key PEM.
// The private key never leaves the file.
func NewKeyFile(path, passphrase string) (publicPEM string, err error) {
	priv, err := Generate()
	if err != nil {
		return "", err
	}
	sealed, err := Encrypt(priv, passphrase)
	if err != nil {
		return "", err
	}
	publicPEM, err = PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	if err != nil {
		return "", err
	}
	return publicPEM, WriteNewKeyFile(path, sealed)
}

// WriteNewKeyFile creates path with 0600 permissions and fails if it exists.
func WriteNewKeyFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the user chooses where the key goes
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%s already exists; choose another --out path or key id", path)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Sign returns the base64 Ed25519 signature over the raw firmware bytes.
func Sign(priv ed25519.PrivateKey, firmware []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, firmware))
}

// MaxPinnedKeys is how many signing keys the 1.0 Arduino and ESP-IDF
// clients pin.
const MaxPinnedKeys = 2

// PinnedKey is one key in a firmware snippet.
type PinnedKey struct {
	KeyID        string
	PublicKeyPEM string
}

// FirmwareSnippet is the signing_keys block for the 1.0 Arduino and ESP-IDF
// clients, pinning the first MaxPinnedKeys keys. With one key it leaves a
// slot for the backup key.
func FirmwareSnippet(keys ...PinnedKey) string {
	keys = keys[:min(len(keys), MaxPinnedKeys)]
	var b strings.Builder
	b.WriteString("static const simpleota_signing_key_t kSigningKeys[] = {\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "    { %q,\n", k.KeyID)
		lines := strings.Split(strings.TrimRight(k.PublicKeyPEM, "\n"), "\n")
		for i, line := range lines {
			sep := ""
			if i == len(lines)-1 {
				sep = " },"
			}
			fmt.Fprintf(&b, "      \"%s\\n\"%s\n", line, sep)
		}
	}
	if len(keys) == 1 {
		b.WriteString("    // { \"backup-key-id\", \"...backup public key PEM...\" },\n")
	}
	b.WriteString("};\n\n")
	b.WriteString("cfg.security_mode = SIMPLEOTA_SECURITY_SIGNED;\n")
	b.WriteString("cfg.signing_keys = kSigningKeys;\n")
	if len(keys) == 1 {
		b.WriteString("cfg.num_signing_keys = 1;  // 2 once the backup key is added\n")
	} else {
		fmt.Fprintf(&b, "cfg.num_signing_keys = %d;\n", len(keys))
	}
	return b.String()
}
