package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		Short:   "Create, list, export and revoke firmware signing keys",
		Long: `Signed releases carry an Ed25519 signature that devices check against the
public keys pinned in their firmware. The private key is made and kept on
your machine, encrypted with a passphrase; only the public key is
registered with the current project, and SimpleOTA never holds a private
key.

Devices pin at most two keys and cannot change them over the air, so make
a second, offline backup key before your first signed release and pin
both.`,
		Example: `  simpleota key generate --key-id prod-2026
  simpleota key generate --key-id backup-2026 --out /Volumes/offline/backup-2026.key
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
	return &cobra.Command{
		Use:   "list",
		Short: "List the public signing keys registered on the project",
		Long: `List the public signing keys registered on the current project, active
and revoked, newest first. --json includes each public key's PEM.`,
		Example: `  simpleota key list
  simpleota key list --json | jq -r '.[] | select(.revoked_at == null) | .key_id'`,
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
			return a.printer().KeyList(views)
		},
	}
}

func (a *App) keyGenerateCommand() *cobra.Command {
	var keyID, out string
	cmd := &cobra.Command{
		Use:   "generate --key-id <id>",
		Short: "Create an Ed25519 signing key on this machine and register its public key",
		Long: `Create an Ed25519 keypair on this machine, save the private key encrypted
with a passphrase (0600, in <config dir>/keys by default), and register
only the public key with the current project. The first key you generate
becomes the default that release signs with.

Prints the signing_keys snippet for the 1.0 Arduino and ESP-IDF clients,
which pin two keys: the new key and the project's newest other active key.

Needs a personal token, so a leaked CI token cannot add signing keys. The
passphrase is prompted for, or read from SIMPLEOTA_SIGNING_KEY_PASSPHRASE.`,
		Example: `  simpleota key generate --key-id prod-2026
  simpleota key generate --key-id backup-2026 --out /Volumes/offline/backup-2026.key`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runKeyGenerate(cmd.Context(), keyID, out)
		},
	}
	cmd.Flags().StringVar(&keyID, "key-id", "", "name for the key on the project, e.g. prod-2026 (required)")
	cmd.Flags().StringVar(&out, "out", "", "where to write the encrypted private key (default: <config dir>/keys/<key-id>.key)")
	return cmd
}

func (a *App) runKeyGenerate(ctx context.Context, keyID, out string) error {
	if keyID == "" {
		return usageErr("--key-id is required, e.g. --key-id prod-2026")
	}
	if !manifest.ValidKeyID(keyID) {
		return usageErr("invalid --key-id %q: use 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", keyID)
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
	for _, k := range existing {
		if k.KeyID == keyID {
			return usageErr("the project already has a signing key named %q; choose another --key-id", keyID)
		}
	}
	if out, err = newKeyPath(s.configDir, keyID, out); err != nil {
		return err
	}
	passphrase, err := a.newPassphrase()
	if err != nil {
		return err
	}
	publicPEM, err := signing.NewKeyFile(out, passphrase)
	if err != nil {
		return err
	}
	if _, err := s.api.RegisterSigningKey(ctx, projectID, keyID, publicPEM); err != nil {
		_ = os.Remove(out)
		return fmt.Errorf("registering the public key failed, so the new key was discarded: %w", err)
	}
	isDefault, err := s.makeDefaultKey(out)
	if err != nil {
		return fmt.Errorf("key %q is registered and saved to %s, but making it the default failed (%w); pass --key-file to sign with it", keyID, out, err)
	}

	pinned, others := pinnedKeys(existing, signing.PinnedKey{KeyID: keyID, PublicKeyPEM: publicPEM})
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
	p := a.printer()
	p.Notice("%s", keyWarning)
	if len(others) > len(pinned)-1 {
		p.Notice("The project has %d other active keys (%s). 1.0 clients pin %d keys, so the snippet pins %s; swap in another key ID if you need it.",
			len(others), strings.Join(others, ", "), signing.MaxPinnedKeys, strings.Join(view.PinnedKeyIDs, " and "))
	}
	return p.KeyGenerated(view)
}

// newKeyPath returns the absolute path for a new key file, defaulting to
// <config dir>/keys/<key-id>.key, and refuses one that exists.
func newKeyPath(configDir, keyID, out string) (string, error) {
	if out == "" {
		out = filepath.Join(configDir, "keys", keyID+".key")
	}
	out, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(out); err == nil {
		return "", usageErr("%s already exists; choose another --out path", out)
	}
	return out, nil
}

// makeDefaultKey makes path the key release signs with when there is no
// default yet, and reports whether it did.
func (s *session) makeDefaultKey(path string) (bool, error) {
	if s.cfg.SigningKeyFile != "" {
		return false, nil
	}
	s.cfg.SigningKeyFile = path
	return true, config.Save(s.configDir, s.cfg)
}

// pinnedKeys returns the keys for the firmware snippet: the new key, then
// the project's newest other active key. It also returns the IDs of all the
// other active keys. Public keys from the server are parsed and re-encoded
// before they go into the snippet's C strings.
func pinnedKeys(existing []api.SigningKey, created signing.PinnedKey) ([]signing.PinnedKey, []string) {
	active := make([]api.SigningKey, 0, len(existing))
	for _, k := range existing {
		if k.RevokedAt == nil && manifest.ValidKeyID(k.KeyID) {
			active = append(active, k)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].CreatedAt.After(active[j].CreatedAt) })
	pinned := []signing.PinnedKey{created}
	var others []string
	for _, k := range active {
		pub, err := signing.ParsePublicKeyPEM(k.PublicKeyPEM)
		if err != nil {
			continue
		}
		others = append(others, k.KeyID)
		if len(pinned) < signing.MaxPinnedKeys {
			pemText, err := signing.PublicKeyPEM(pub)
			if err == nil {
				pinned = append(pinned, signing.PinnedKey{KeyID: k.KeyID, PublicKeyPEM: pemText})
			}
		}
	}
	return pinned, others
}

func (a *App) keyRevokeCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <key-id> --yes",
		Short: "Revoke a public signing key on the project",
		Long: `Revoke a signing key on the current project, for a key that leaked or is
retired. The server then refuses new uploads signed with it. Builds it
already verified stay servable, and the private key file on this machine
is left alone.

Revoking cannot be undone. Devices keep the keys pinned in their firmware,
so before revoking, ship firmware that pins the key you will sign with
next: a device that pins only the revoked key cannot verify a release
signed with any other.

Needs a personal token, and --yes to confirm.`,
		Example: `  simpleota key revoke prod-2025 --yes`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return usageErr("revoking %s cannot be undone; pass --yes to confirm", args[0])
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
		Short: "Print the private signing key, encrypted or as PEM for a CI secret",
		Long: `Print a private signing key to stdout, to store as a CI secret. It reads
the key file on this machine and calls no API.

--format encrypted (the default) prints the passphrase-protected key file;
set it as SIMPLEOTA_SIGNING_KEY and the passphrase as
SIMPLEOTA_SIGNING_KEY_PASSPHRASE in CI. --format pem prints the
unencrypted PKCS#8 PEM, for a secret store that encrypts on its own.`,
		Example: `  simpleota key export | gh secret set SIMPLEOTA_SIGNING_KEY
  simpleota key export --format pem --key-file ~/keys/prod-2026.key`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runKeyExport(keyFile, format)
		},
	}
	cmd.Flags().StringVar(&keyFile, "key-file", "", "key file to export (default: the key key generate set as default)")
	cmd.Flags().StringVar(&format, "format", "encrypted", "encrypted or pem")
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
			return usageErr("no default signing key; pass --key-file")
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
			return errors.New("that key file is not encrypted; export it with --format pem")
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
		return "", usageErr("set %s or run in a terminal to enter the key passphrase", passphraseEnv)
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
			return "", fmt.Errorf("%w: set %s", signing.ErrPassphraseRequired, passphraseEnv)
		}
		return a.Prompt("Signing key passphrase: ")
	}
}
