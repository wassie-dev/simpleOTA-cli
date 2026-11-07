package cli

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

const passphraseEnv = "SIMPLEOTA_SIGNING_KEY_PASSPHRASE"

const keyWarning = `WARNING: this is your firmware signing key.
  Lose it and your fleet can never be updated again.
  Leak it and anyone can sign firmware your devices will install.
Keep a second key offline and pin it in your firmware as the backup.`

func (a *App) keyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "key",
		Aliases: []string{"keys"},
		Short:   "Create, list, export and revoke signing keys",
		Long: `Signed releases carry an Ed25519 signature, which devices check against the
public keys pinned in their firmware.

The private key is made and kept on your machine, encrypted with a
passphrase. Only the public key is registered with the project; SimpleOTA
never holds a private key.

Devices pin at most two keys and cannot change them over the air. Before your
first signed release, make a second, offline backup key and pin both.`,
		Example: `  simpleota key generate --key-id prod-2026
  simpleota key generate --key-id backup-2026 --out /Volumes/usb/backup.key
  simpleota key list
  simpleota key export | gh secret set SIMPLEOTA_SIGNING_KEY
  simpleota key revoke prod-2025 --yes`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	cmd.AddCommand(a.keyListCommand(), a.keyGenerateCommand(), a.keyExportCommand(), a.keyRevokeCommand())
	return cmd
}

func (a *App) keyListCommand() *cobra.Command {
	return withLimit(&cobra.Command{
		Use:   "list",
		Short: "List the project's public signing keys",
		Long: `List the public signing keys registered on the current project, active and
revoked, newest first. With --json, each key includes its PEM.`,
		Example: `  simpleota key list
  simpleota key list --json | jq -r '.[].key_id'`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			keys, err := s.api.ListSigningKeys(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			sort.SliceStable(keys, func(i, j int) bool { return keys[i].CreatedAt.After(keys[j].CreatedAt) })
			views := viewsOf(keys, output.NewSigningKeyView)
			return printList(a, cmd, views, a.printer().KeyList)
		},
	})
}

func (a *App) keyGenerateCommand() *cobra.Command {
	var keyID, out string
	cmd := &cobra.Command{
		Use:   "generate --key-id <id>",
		Short: "Create a signing key and register its public key",
		Long: `Create an Ed25519 key on this machine and register its public key with the
current project. The private key is saved encrypted with a passphrase, with
0600 permissions. The first key you generate becomes the default for signing
builds.

The output includes the signing_keys snippet for the 1.0 Arduino and ESP-IDF
clients. It pins two keys: the new one and your default signing key, or the
project's newest other active key when the default is not one of them. A
default key set by an older version at a path of your choosing is read once,
with its passphrase, to recognise it.

If registering the public key fails without a clear refusal, the encrypted key
is kept. Run the same command again to finish: it reads the existing key file
and registers its public key only if the project does not have it yet.

Needs a personal token, so a leaked CI token cannot add signing keys. You are
asked for the passphrase unless SIMPLEOTA_SIGNING_KEY_PASSPHRASE is set.`,
		Example: `  simpleota key generate --key-id prod-2026
  simpleota key generate --key-id backup-2026 --out /Volumes/usb/backup.key`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runKeyGenerate(cmd.Context(), keyID, out)
		},
	}
	cmd.Flags().StringVar(&keyID, "key-id", "", "`id` for the key on the project, e.g. prod-2026 (required)")
	cmd.Flags().StringVar(&out, "out", "", "`path` of the encrypted private key (default: in the config directory)")
	return cmd
}

func (a *App) runKeyGenerate(ctx context.Context, keyID, out string) error {
	if keyID == "" {
		return usageErr("--key-id is required, e.g. --key-id prod-2026")
	}
	if !manifest.ValidKeyID(keyID) {
		return usageErr("invalid --key-id %q\nUse 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit.", keyID)
	}
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return err
	}
	if err := s.requirePersonal("key generate"); err != nil {
		return err
	}
	existing, err := s.api.ListSigningKeys(ctx, projectID)
	if err != nil {
		return err
	}
	if out, err = keyPath(s.configDir, keyID, out); err != nil {
		return err
	}
	p := a.printer()
	registered := findKey(existing, keyID)
	var publicPEM string
	created := false
	if _, err := os.Lstat(out); err == nil {
		if publicPEM, err = a.resumeKey(existing, keyID, out); err != nil {
			return err
		}
		p.Notice("Using the existing key file %s.", out)
	} else {
		if registered != nil {
			return usageErr("the project already has a signing key named %q\nChoose another --key-id.", keyID)
		}
		passphrase, err := a.newPassphrase()
		if err != nil {
			return err
		}
		if publicPEM, err = signing.NewKeyFile(out, passphrase); err != nil {
			return err
		}
		created = true
	}
	if registered == nil {
		err := registerKey(ctx, s.api, p, projectID, keyID, publicPEM)
		if errors.Is(err, errKeyRefused) && created {
			_ = os.Remove(out)
			return fmt.Errorf("%w, so the new key was discarded", err)
		}
		if err != nil {
			return fmt.Errorf("%w\nThe encrypted key is kept at %s.\nRun the same command again to finish. It registers the key only if the\nproject does not have it yet.", err, out)
		}
	}
	isDefault, err := s.makeDefaultKey(out, publicPEM)
	if err != nil {
		return fmt.Errorf("key %q is registered and saved to %s\nMaking it the default failed: %w\nPass --key-file to sign with it.", keyID, out, err)
	}
	isDefault = isDefault || s.cfg.SigningKeyFile == out
	if !isDefault && s.cfg.SigningKeyPublicPEM == "" && !slices.ContainsFunc(existing, s.isDefaultKey) {
		a.recordDefaultPublicKey(s)
	}

	pinned, others, defaultPinned := pinnedKeys(existing, signing.PinnedKey{KeyID: keyID, PublicKeyPEM: publicPEM}, s.isDefaultKey)
	view := output.KeyView{
		KeyID:             keyID,
		ProjectID:         projectID,
		PublicKeyPEM:      publicPEM,
		PrivateKeyFile:    out,
		DefaultSigningKey: isDefault,
		FirmwareSnippet:   signing.FirmwareSnippet(pinned...),
		PinnedKeyIDs:      make([]string, len(pinned)),
	}
	for i, k := range pinned {
		view.PinnedKeyIDs[i] = k.KeyID
	}
	p.Notice("%s", keyWarning)
	createdIsDefault := isDefault || s.isDefaultKey(api.SigningKey{KeyID: keyID, PublicKeyPEM: publicPEM})
	if !createdIsDefault && !defaultPinned && len(pinned) > 1 {
		p.Notice("Note: none of the project's active keys is recognised as your default signing\nkey, so the snippet pins %s, the newest. Check that you hold its private key.", pinned[1].KeyID)
	}
	if len(others) > len(pinned)-1 {
		p.Notice("Note: the project has %d other active keys (%s), but 1.0 clients pin %d.\nThe snippet pins %s. Swap in another key if you need it.",
			len(others), strings.Join(others, ", "), signing.MaxPinnedKeys, strings.Join(view.PinnedKeyIDs, " and "))
	}
	return p.KeyGenerated(view)
}

// errKeyRefused means the server answered that it did not register the key.
var errKeyRefused = errors.New("the server refused the public key")

// registerKey registers publicPEM as keyID. When the outcome is unknown,
// because the reply was lost or was a server error, it lists the project's
// keys to see whether the key was stored. It returns errKeyRefused only when
// the key is known not to be registered.
func registerKey(ctx context.Context, c api.API, p output.Printer, projectID, keyID, publicPEM string) error {
	_, err := c.RegisterSigningKey(ctx, projectID, keyID, publicPEM)
	if err == nil {
		return nil
	}
	if !registrationUncertain(err) {
		return fmt.Errorf("%w: %w", errKeyRefused, err)
	}
	keys, listErr := c.ListSigningKeys(ctx, projectID)
	if listErr == nil {
		if k := findKey(keys, keyID); k != nil {
			if !signing.SamePublicKey(k.PublicKeyPEM, publicPEM) {
				return fmt.Errorf("%w: the project now has a different key named %q", errKeyRefused, keyID)
			}
			p.Notice("The reply to the registration was lost, but the project has key %s.", keyID)
			return nil
		}
	}
	return fmt.Errorf("could not confirm that the project registered key %q: %w", keyID, err)
}

// registrationUncertain reports whether the server may have stored a key
// despite err: anything but a 4xx answer that is not a timeout or rate limit.
func registrationUncertain(err error) bool {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.StatusCode >= 500 || ae.StatusCode == http.StatusRequestTimeout || ae.StatusCode == http.StatusTooManyRequests
	}
	return true
}

// resumeKey reads the public key of an existing key file, to finish a key
// generate whose registration did not complete. It refuses a file whose key
// the project holds under another ID, or whose ID the project gave to
// another key.
func (a *App) resumeKey(existing []api.SigningKey, keyID, path string) (string, error) {
	data, err := signing.ReadKeyFile(path)
	if err != nil {
		return "", err
	}
	priv, err := signing.ParsePrivateKey(data, a.passphraseSource())
	if err != nil {
		return "", err
	}
	publicPEM, err := signing.PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	if err != nil {
		return "", err
	}
	for _, k := range existing {
		same := signing.SamePublicKey(k.PublicKeyPEM, publicPEM)
		switch {
		case k.KeyID == keyID && !same:
			return "", usageErr("the project already has a signing key named %q, and it is not the key in %s\nChoose another --key-id or --out.", keyID, path)
		case k.KeyID == keyID && k.RevokedAt != nil:
			return "", usageErr("signing key %q is revoked on the project\nChoose another --key-id and --out.", keyID)
		case k.KeyID != keyID && same:
			return "", usageErr("the key in %s is already registered as %q\nChoose another --out path.", path, k.KeyID)
		}
	}
	return publicPEM, nil
}

func findKey(keys []api.SigningKey, keyID string) *api.SigningKey {
	for i := range keys {
		if keys[i].KeyID == keyID {
			return &keys[i]
		}
	}
	return nil
}

// keyPath returns the absolute path for a key file, defaulting to
// <config dir>/keys/<key-id>.key.
func keyPath(configDir, keyID, out string) (string, error) {
	if out == "" {
		out = defaultKeyPath(configDir, keyID)
	}
	return filepath.Abs(out)
}

func defaultKeyPath(configDir, keyID string) string {
	return filepath.Join(configDir, "keys", keyID+".key")
}

// makeDefaultKey makes path the key release signs with when there is no
// default yet, and reports whether it did.
func (s *session) makeDefaultKey(path, publicPEM string) (bool, error) {
	made := false
	err := s.updateConfig(func(c *config.Config) bool {
		if made = c.SigningKeyFile == ""; made {
			c.SigningKeyFile, c.SigningKeyPublicPEM = path, publicPEM
		}
		return made
	})
	return made, err
}

// updateConfig changes the config through config.Update, under the lock
// every simpleota process shares, so that a command waiting at a prompt does
// not undo what another command saved meanwhile.
func (s *session) updateConfig(change func(*config.Config) bool) error {
	cfg, err := config.Update(s.configDir, change)
	s.cfg = cfg
	return err
}

// recordDefaultPublicKey saves the public key of a default signing key set
// before the CLI recorded one, so the key can be recognised among the
// project's keys. An encrypted key needs its passphrase, from the
// environment or the terminal; without it nothing is saved.
func (a *App) recordDefaultPublicKey(s *session) {
	path := s.cfg.SigningKeyFile
	passphrase := func() (string, error) {
		if p := a.Getenv(passphraseEnv); p != "" {
			return p, nil
		}
		if a.Prompt == nil {
			return "", signing.ErrPassphraseRequired
		}
		p, err := a.Prompt(fmt.Sprintf("Passphrase of your default signing key %s, to recognise it (Enter to skip): ", path))
		if err == nil && p == "" {
			err = errors.New("skipped")
		}
		return p, err
	}
	data, err := signing.ReadKeyFile(path)
	if err != nil {
		return
	}
	priv, err := signing.ParsePrivateKey(data, passphrase)
	if err != nil {
		a.printer().Notice("Your default signing key %s could not be read (%v), so it is not recognised.", path, err)
		return
	}
	publicPEM, err := signing.PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	if err != nil {
		return
	}
	if err := s.updateConfig(func(c *config.Config) bool {
		if c.SigningKeyFile != path {
			return false
		}
		c.SigningKeyPublicPEM = publicPEM
		return true
	}); err != nil {
		a.printer().Notice("Recording the public key of your default signing key failed: %v", err)
	}
}

// isDefaultKey reports whether k is the default signing key: by its public
// key, or, for a config that does not record it, by the file name key
// generate gives a key.
func (s *session) isDefaultKey(k api.SigningKey) bool {
	if s.cfg.SigningKeyPublicPEM != "" {
		return signing.SamePublicKey(k.PublicKeyPEM, s.cfg.SigningKeyPublicPEM)
	}
	return s.cfg.SigningKeyFile != "" && s.cfg.SigningKeyFile == defaultKeyPath(s.configDir, k.KeyID)
}

// pinnedKeys returns the keys for the firmware snippet: the new key, then
// the default signing key if it is another active project key, or else the
// project's newest other active key. It also returns the IDs of all the
// other active keys, and whether the default key was pinned. Public keys
// from the server are parsed and re-encoded before they go into the
// snippet's C strings.
func pinnedKeys(existing []api.SigningKey, created signing.PinnedKey, isDefault func(api.SigningKey) bool) ([]signing.PinnedKey, []string, bool) {
	var active []signing.PinnedKey
	var others []string
	second := -1
	sorted := slices.Clone(existing)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	for _, k := range sorted {
		if k.RevokedAt != nil || k.KeyID == created.KeyID || !manifest.ValidKeyID(k.KeyID) {
			continue
		}
		pub, err := signing.ParsePublicKeyPEM(k.PublicKeyPEM)
		if err != nil {
			continue
		}
		pemText, err := signing.PublicKeyPEM(pub)
		if err != nil {
			continue
		}
		if second < 0 && isDefault(k) {
			second = len(active)
		}
		active = append(active, signing.PinnedKey{KeyID: k.KeyID, PublicKeyPEM: pemText})
		others = append(others, k.KeyID)
	}
	pinned := []signing.PinnedKey{created}
	switch {
	case second >= 0:
		pinned = append(pinned, active[second])
	case len(active) > 0:
		pinned = append(pinned, active[0])
	}
	return pinned, others, second >= 0
}

func (a *App) keyRevokeCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <key-id> --yes",
		Short: "Revoke a public signing key on the project",
		Long: `Revoke a signing key on the current project, for a key that leaked or is
retired. The server then refuses new uploads signed with it. Builds it already
verified can still be served, and the private key file on this machine is
left alone.

This cannot be undone. Devices keep the keys pinned in their firmware, so
first ship firmware that pins the key you will sign with next. A device that
pins only the revoked key cannot verify a release signed with any other.

Needs a personal token, and --yes to confirm.`,
		Example: `  simpleota key revoke prod-2025 --yes`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return usageErr("revoking %s cannot be undone\nPass --yes to confirm.", args[0])
			}
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			if err := s.requirePersonal("key revoke"); err != nil {
				return err
			}
			if err := s.api.RevokeSigningKey(cmd.Context(), projectID, args[0]); err != nil {
				return err
			}
			return a.printer().Revoked(output.RevokedView{KeyID: args[0], ProjectID: projectID})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the revocation")
	return cmd
}

func (a *App) keyExportCommand() *cobra.Command {
	var keyFile, format string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Print a private signing key, to store as a CI secret",
		Long: `Print a private signing key to stdout, to store as a CI secret. This reads the
key file on this machine and does not call the API.

Formats:
  encrypted  The passphrase-protected key file (the default). In CI, set it as
             SIMPLEOTA_SIGNING_KEY, and the passphrase as
             SIMPLEOTA_SIGNING_KEY_PASSPHRASE.
  pem        The unencrypted PKCS#8 PEM, for a secret store that encrypts on
             its own.`,
		Example: `  simpleota key export | gh secret set SIMPLEOTA_SIGNING_KEY
  simpleota key export --format pem --key-file ~/keys/prod-2026.key`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runKeyExport(keyFile, format)
		},
	}
	cmd.Flags().StringVar(&keyFile, "key-file", "", "`path` of the key file to export (default: your default signing key)")
	cmd.Flags().StringVar(&format, "format", "encrypted", "output `format`: encrypted or pem")
	return cmd
}

func (a *App) runKeyExport(keyFile, format string) error {
	if format != "encrypted" && format != "pem" {
		return usageErr("--format must be encrypted or pem")
	}
	if keyFile == "" {
		dir, err := a.configDir()
		if err != nil {
			return err
		}
		cfg, err := config.Load(dir)
		if err != nil {
			return err
		}
		if cfg.SigningKeyFile == "" {
			return usageErr("no default signing key\nPass --key-file <path>.")
		}
		keyFile = cfg.SigningKeyFile
	}
	data, err := signing.ReadKeyFile(keyFile)
	if err != nil {
		return err
	}
	var exported string
	if format == "encrypted" {
		if !signing.IsEncrypted(data) {
			return errors.New("that key file is not encrypted\nExport it with --format pem.")
		}
		exported = string(data)
	} else {
		priv, err := signing.ParsePrivateKey(data, a.passphraseSource())
		if err != nil {
			return err
		}
		pemBytes, err := signing.PrivateKeyPEM(priv)
		if err != nil {
			return err
		}
		exported = string(pemBytes)
	}
	p := a.printer()
	p.Notice("%s", keyWarning)
	return p.Exported(output.ExportView{Format: format, PrivateKey: exported})
}

// newPassphrase reads and confirms a passphrase for a new key.
func (a *App) newPassphrase() (string, error) {
	if p := a.Getenv(passphraseEnv); p != "" {
		if err := signing.CheckPassphrase(p); err != nil {
			return "", usageErr("%s: %v", passphraseEnv, err)
		}
		return p, nil
	}
	if a.Prompt == nil {
		return "", usageErr("no passphrase for the new key\nSet %s, or run this in a terminal to be asked for it.", passphraseEnv)
	}
	p, err := a.Prompt(fmt.Sprintf("Passphrase for the new key (at least %d characters): ", signing.MinPassphraseLen))
	if err != nil {
		return "", err
	}
	if err := signing.CheckPassphrase(p); err != nil {
		return "", usageErr("%v", err)
	}
	again, err := a.Prompt("Repeat the passphrase: ")
	if err != nil {
		return "", err
	}
	if again != p {
		return "", usageErr("the passphrases do not match")
	}
	return p, nil
}

// passphraseSource reads an existing key's passphrase from the environment or
// the terminal, only when the key turns out to be encrypted.
func (a *App) passphraseSource() signing.PassphraseFunc {
	return func() (string, error) {
		if p := a.Getenv(passphraseEnv); p != "" {
			return p, nil
		}
		if a.Prompt == nil {
			return "", fmt.Errorf("%w\nSet %s.", signing.ErrPassphraseRequired, passphraseEnv)
		}
		return a.Prompt("Signing key passphrase: ")
	}
}
