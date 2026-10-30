package cli

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/config"
	"github.com/wassie-dev/simpleOTA-cli/internal/credentials"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

const passphraseEnv = "SIMPLEOTA_SIGNING_KEY_PASSPHRASE"

const keyWarning = `WARNING: this is your firmware signing key.
  Lose it and your fleet can never be updated again.
  Leak it and anyone can sign firmware your devices will install.
Keep a second key offline and pin it in your firmware as the backup.`

func (a *App) keysCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "keys",
		Aliases: []string{"key"},
		Short:   "Create and export firmware signing keys",
		Args:    cobra.ArbitraryArgs,
		RunE:    groupRun,
	}
	cmd.AddCommand(a.keysGenerateCommand(), a.keysExportCommand())
	return cmd
}

// requirePersonal refuses project tokens for commands that change who can
// sign firmware.
func (s *session) requirePersonal(what string) error {
	if s.kind != credentials.KindPersonal {
		return authErr("%s needs a personal token (soto_pat_...), so a leaked CI token cannot add signing keys; log in with a personal token from the dashboard", what)
	}
	return nil
}

func (a *App) keysGenerateCommand() *cobra.Command {
	var keyID, out string
	cmd := &cobra.Command{
		Use:   "generate --key-id <id>",
		Short: "Create an Ed25519 signing key on this machine and register its public key",
		Long: `Create an Ed25519 keypair locally, save the private key encrypted with a
passphrase (0600), and register only the public key with the project.
Prints the signing_keys snippet for the 1.0 Arduino and ESP-IDF clients,
which pin two keys: the new key and the project's newest other active key.

Needs a personal token. The passphrase is prompted for, or read from
SIMPLEOTA_SIGNING_KEY_PASSPHRASE.`,
		Example: `  simpleota keys generate --key-id prod-2026
  simpleota keys generate --key-id backup-2026 --out /Volumes/offline/backup-2026.key`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runKeysGenerate(cmd.Context(), keyID, out)
		},
	}
	cmd.Flags().StringVar(&keyID, "key-id", "", "name for the key on the project, e.g. prod-2026 (required)")
	cmd.Flags().StringVar(&out, "out", "", "where to write the encrypted private key (default: <config dir>/keys/<key-id>.key)")
	return cmd
}

func (a *App) runKeysGenerate(ctx context.Context, keyID, out string) error {
	if keyID == "" {
		return usageErr("--key-id is required, e.g. --key-id prod-2026")
	}
	if !manifest.ValidKeyID(keyID) {
		return usageErr("invalid --key-id %q: use 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", keyID)
	}
	s, err := a.openSession()
	if err != nil {
		return err
	}
	if err := s.requirePersonal("keys generate"); err != nil {
		return err
	}
	projectID, err := s.projectID(ctx)
	if err != nil {
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
	if out == "" {
		out = filepath.Join(s.configDir, "keys", keyID+".key")
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if _, err := os.Lstat(out); err == nil {
		return usageErr("%s already exists; choose another --out path", out)
	}

	passphrase, err := a.newPassphrase()
	if err != nil {
		return err
	}
	priv, err := signing.Generate()
	if err != nil {
		return err
	}
	sealed, err := signing.Encrypt(priv, passphrase)
	if err != nil {
		return err
	}
	publicPEM, err := signing.PublicKeyPEM(priv.Public().(ed25519.PublicKey))
	if err != nil {
		return err
	}
	if err := signing.WriteNewKeyFile(out, sealed); err != nil {
		return err
	}
	if _, err := s.api.RegisterSigningKey(ctx, projectID, keyID, publicPEM); err != nil {
		_ = os.Remove(out)
		return fmt.Errorf("registering the public key failed, so the new key was discarded: %w", err)
	}

	isDefault := false
	if s.cfg.SigningKeyFile == "" {
		s.cfg.SigningKeyFile = out
		if err := config.Save(s.configDir, s.cfg); err != nil {
			return fmt.Errorf("key %q is registered and saved to %s, but making it the default failed (%w); pass --key-file to sign with it", keyID, out, err)
		}
		isDefault = true
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
	if p.JSON {
		return p.WriteJSON(view)
	}
	fmt.Fprintf(a.Out, "Registered signing key %q with the project.\n", keyID)
	fmt.Fprintf(a.Out, "Encrypted private key: %s\n", out)
	if isDefault {
		fmt.Fprintln(a.Out, "simpleota release signs with this key by default.")
	} else {
		fmt.Fprintf(a.Out, "Sign with it using: simpleota release --key-file %s ...\n", out)
	}
	fmt.Fprintf(a.Out, "\nPublic key:\n%s\n", publicPEM)
	if len(pinned) == 1 {
		fmt.Fprintf(a.Out, "Pin it in your firmware (1.0 clients):\n\n%s", view.FirmwareSnippet)
		fmt.Fprintln(a.Out, "\nNext: run keys generate again for an offline backup key, and pin both keys before your first signed release.")
		return nil
	}
	fmt.Fprintf(a.Out, "Pin %s in your firmware (1.0 clients):\n\n%s", strings.Join(view.PinnedKeyIDs, " and "), view.FirmwareSnippet)
	return nil
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

func (a *App) keysExportCommand() *cobra.Command {
	var keyFile, format string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Print the private signing key, encrypted or as PEM for a CI secret",
		Long: `Print the private key to stdout. --format encrypted (the default) prints
the passphrase-protected key file; pair it with
SIMPLEOTA_SIGNING_KEY_PASSPHRASE in CI. --format pem prints the
unencrypted PKCS#8 PEM.`,
		Example: `  simpleota keys export | gh secret set SIMPLEOTA_SIGNING_KEY
  simpleota keys export --format pem --key-file ~/keys/prod-2026.key`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runKeysExport(keyFile, format)
		},
	}
	cmd.Flags().StringVar(&keyFile, "key-file", "", "key file to export (default: the key keys generate set as default)")
	cmd.Flags().StringVar(&format, "format", "encrypted", "encrypted or pem")
	return cmd
}

func (a *App) runKeysExport(keyFile, format string) error {
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
	if p.JSON {
		return p.WriteJSON(output.ExportView{Format: format, PrivateKey: exported})
	}
	_, err = fmt.Fprint(a.Out, exported)
	return err
}
