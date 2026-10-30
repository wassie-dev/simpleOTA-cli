package cli

import (
	"context"
	"crypto/ed25519"
	"errors"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/release"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

const signingKeyEnv = "SIMPLEOTA_SIGNING_KEY"

type releaseFlags struct {
	versionLabel   string
	chip           string
	framework      string
	percent        int
	channel        string
	keyFile        string
	securityMode   string
	allowDuplicate bool
}

func (a *App) releaseCommand() *cobra.Command {
	var f releaseFlags
	cmd := &cobra.Command{
		Use:   "release <bin> --version-label <label>",
		Short: "Sign, upload and deploy a firmware binary in one step",
		Long: `Upload an application binary and start a deployment to a channel in one
call. The server assigns the build number; devices are offered only builds
higher than the one they run.

The chip family comes from the image header, which names the chip the
image was built for. A --chip that differs from it is refused unless a
device in the project reports that chip, since no other device would be
offered the build.

When another deployment for the same chip is still active on the channel,
release names it. A device installs the highest build offered to it, so
the older deployment goes on reaching the devices this release does not.

The release is signed when a key is available: --key-file, then
SIMPLEOTA_SIGNING_KEY (an exported key; set SIMPLEOTA_SIGNING_KEY_PASSPHRASE
for an encrypted one), then the default key from keys generate.

The manifest carries the binary's SHA-256, and the server rejects the upload
if the bytes it received differ. The upload is sent once and never retried:
every accepted upload uses a new build number and counts against the plan's
monthly limit. release stops before uploading when the project already has a
build of the same binary, so running it again after a failed upload cannot
create a second build; pass --allow-duplicate to upload it anyway.`,
		Example: `  simpleota release build/app.bin --version-label 1.4.0
  simpleota release build/app.bin --version-label 1.4.1 --percent 10 --channel beta`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runRelease(cmd.Context(), args[0], f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.versionLabel, "version-label", "", "human-readable version, e.g. 1.4.0 (required)")
	fl.StringVar(&f.chip, "chip", "", "chip family the build targets, e.g. esp32s3 (default: the chip the image header names)")
	fl.StringVar(&f.framework, "framework", "", "arduino or esp_idf (default: the project's default framework)")
	fl.IntVar(&f.percent, "percent", 100, "percentage of the channel's devices to offer the build to")
	fl.StringVar(&f.channel, "channel", "stable", "release channel; it must already exist on the project")
	fl.StringVar(&f.keyFile, "key-file", "", "signing key file")
	fl.StringVar(&f.securityMode, "security-mode", "auto", "auto (signed when a key is available), basic or signed")
	fl.BoolVar(&f.allowDuplicate, "allow-duplicate", false, "upload even if the project already has a build of this binary")
	return cmd
}

func (a *App) runRelease(ctx context.Context, binPath string, f releaseFlags) error {
	if f.versionLabel == "" {
		return usageErr("--version-label is required")
	}
	if f.framework != "" && f.framework != manifest.FrameworkArduino && f.framework != manifest.FrameworkESPIDF {
		return usageErr("--framework must be arduino or esp_idf")
	}
	if f.percent < 0 || f.percent > 100 {
		return usageErr("--percent must be between 0 and 100")
	}
	switch f.securityMode {
	case "auto", manifest.SecurityBasic, manifest.SecuritySigned:
	default:
		return usageErr("--security-mode must be auto, basic or signed")
	}

	s, err := a.openSession()
	if err != nil {
		return err
	}
	var key ed25519.PrivateKey
	if f.securityMode != manifest.SecurityBasic {
		key, err = a.loadSigningKey(f.keyFile, s.cfg.SigningKeyFile)
		if err != nil {
			return err
		}
		if key == nil && f.securityMode == manifest.SecuritySigned {
			return usageErr("--security-mode signed needs a key: pass --key-file or set %s", signingKeyEnv)
		}
	}
	projectID, err := s.projectID(ctx)
	if err != nil {
		return err
	}
	p := a.printer()
	if key == nil {
		p.Notice("Uploading unsigned (basic). Devices that enforce signed firmware will not be offered this build.")
	}
	svc := release.Service{API: s.api, ProjectID: projectID}
	res, err := svc.Release(ctx, release.Options{
		BinaryPath:     binPath,
		VersionLabel:   f.versionLabel,
		Chip:           f.chip,
		Framework:      f.framework,
		Percent:        f.percent,
		Channel:        f.channel,
		Key:            key,
		AllowDuplicate: f.allowDuplicate,
	})
	var chipErr *release.ChipError
	if errors.As(err, &chipErr) {
		return &exitError{code: ExitUsage, err: err}
	}
	if err != nil {
		return err
	}
	view := output.NewReleaseView(res)
	others, err := svc.AlsoActive(ctx, res)
	if err != nil {
		p.Notice("Released, but checking for other active deployments failed: %v", err)
	}
	for _, d := range others {
		view.AlsoActive = append(view.AlsoActive, output.NewDeploymentView(d))
	}
	return p.Release(view)
}

// loadSigningKey applies --key-file, then SIMPLEOTA_SIGNING_KEY, then the
// configured default. It returns nil when no key is available.
func (a *App) loadSigningKey(flagFile, defaultFile string) (ed25519.PrivateKey, error) {
	var data []byte
	var err error
	switch {
	case flagFile != "":
		data, err = signing.ReadKeyFile(flagFile)
	case a.Getenv(signingKeyEnv) != "":
		data = []byte(a.Getenv(signingKeyEnv))
	case defaultFile != "":
		data, err = signing.ReadKeyFile(defaultFile)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	key, err := signing.ParsePrivateKey(data, a.passphraseSource())
	if errors.Is(err, signing.ErrPassphraseRequired) {
		return nil, usageErr("%v", err)
	}
	return key, err
}
