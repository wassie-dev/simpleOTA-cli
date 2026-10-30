package cli

import (
	"context"
	"crypto/ed25519"
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/release"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
)

const signingKeyEnv = "SIMPLEOTA_SIGNING_KEY"

// uploadHelp describes how release and build upload check, sign and send a
// build.
const uploadHelp = `The chip family comes from the image header, which names the chip the
image was built for. A --chip that differs from it is refused unless a
device in the project reports that chip, since no other device would be
offered the build.

The build is signed when a key is available: --key-file, then
SIMPLEOTA_SIGNING_KEY (an exported key; set SIMPLEOTA_SIGNING_KEY_PASSPHRASE
for an encrypted one), then the default key from key generate.

The manifest carries the binary's SHA-256, and the server rejects the upload
if the bytes it received differ. The upload is sent once and never retried:
every accepted upload uses a new build number and counts against the plan's
monthly limit. The command stops before uploading when the project already
has a build of the same binary, so running it again after a failed upload
cannot create a second build; pass --allow-duplicate to upload it anyway.`

// buildFlags are the flags release and build upload share.
type buildFlags struct {
	versionLabel   string
	chip           string
	framework      string
	keyFile        string
	securityMode   string
	allowDuplicate bool
}

func (f *buildFlags) register(fl *pflag.FlagSet) {
	fl.StringVar(&f.versionLabel, "version-label", "", "human-readable version, e.g. 1.4.0 (required)")
	fl.StringVar(&f.chip, "chip", "", "chip family the build targets, e.g. esp32s3 (default: the chip the image header names)")
	fl.StringVar(&f.framework, "framework", "", "arduino or esp_idf (default: the project's default framework)")
	fl.StringVar(&f.keyFile, "key-file", "", "signing key file")
	fl.StringVar(&f.securityMode, "security-mode", "auto", "auto (signed when a key is available), basic or signed")
	fl.BoolVar(&f.allowDuplicate, "allow-duplicate", false, "upload even if the project already has a build of this binary")
}

func (f buildFlags) validate() error {
	if f.versionLabel == "" {
		return usageErr("--version-label is required")
	}
	if f.framework != "" && f.framework != manifest.FrameworkArduino && f.framework != manifest.FrameworkESPIDF {
		return usageErr("--framework must be arduino or esp_idf")
	}
	switch f.securityMode {
	case "auto", manifest.SecurityBasic, manifest.SecuritySigned:
		return nil
	}
	return usageErr("--security-mode must be auto, basic or signed")
}

// buildJob is a validated upload: the release service for the current
// project and the options to send.
type buildJob struct {
	svc  release.Service
	opts release.Options
}

// prepareBuild validates the flags, loads the signing key and resolves the
// current project.
func (a *App) prepareBuild(ctx context.Context, binPath string, f buildFlags) (buildJob, error) {
	if err := f.validate(); err != nil {
		return buildJob{}, err
	}
	s, err := a.openSession()
	if err != nil {
		return buildJob{}, err
	}
	var key ed25519.PrivateKey
	if f.securityMode != manifest.SecurityBasic {
		key, err = a.loadSigningKey(f.keyFile, s.cfg.SigningKeyFile)
		if err != nil {
			return buildJob{}, err
		}
		if key == nil && f.securityMode == manifest.SecuritySigned {
			return buildJob{}, usageErr("--security-mode signed needs a key: pass --key-file or set %s", signingKeyEnv)
		}
	}
	projectID, err := s.projectID(ctx)
	if err != nil {
		return buildJob{}, err
	}
	return buildJob{
		svc: release.Service{API: s.api, ProjectID: projectID},
		opts: release.Options{
			BinaryPath:     binPath,
			VersionLabel:   f.versionLabel,
			Chip:           f.chip,
			Framework:      f.framework,
			Key:            key,
			AllowDuplicate: f.allowDuplicate,
		},
	}, nil
}

// buildErr gives a chip refusal the usage exit code.
func buildErr(err error) error {
	var chipErr *release.ChipError
	if errors.As(err, &chipErr) {
		return &exitError{code: ExitUsage, err: err}
	}
	return err
}

// noticeUnsigned warns after an upload that went out unsigned.
func noticeUnsigned(p output.Printer, res release.Result) {
	if res.SecurityMode == manifest.SecurityBasic {
		p.Notice("Uploaded unsigned (basic). Devices that enforce signed firmware will not be offered this build.")
	}
}

// alsoActive lists the other active deployments reaching the same devices,
// printing a notice instead of failing when the check itself fails.
func alsoActive(ctx context.Context, p output.Printer, svc release.Service, deploymentID, chip string) []output.DeploymentView {
	views := []output.DeploymentView{}
	others, err := svc.AlsoActive(ctx, deploymentID, chip)
	if err != nil {
		p.Notice("Checking for other active deployments failed: %v", err)
	}
	for _, d := range others {
		views = append(views, output.NewDeploymentView(d))
	}
	return views
}

func (a *App) releaseCommand() *cobra.Command {
	var f buildFlags
	var percent int
	var channel string
	cmd := &cobra.Command{
		Use:   "release <bin> --version-label <label>",
		Short: "Sign, upload and deploy a firmware binary in one step",
		Long: `Upload an application binary to the current project and start a
deployment to a channel in one call. The server assigns the build number;
devices are offered only builds higher than the one they run. To upload
without deploying, use simpleota build upload, then
simpleota deployment create.

When another deployment for the same chip is still active on the channel,
release names it. A device installs the highest build offered to it, so
the older deployment goes on reaching the devices this release does not.

` + uploadHelp,
		Example: `  simpleota release build/app.bin --version-label 1.4.0
  simpleota release build/app.bin --version-label 1.4.1 --percent 10 --channel beta`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if percent < 0 || percent > 100 {
				return usageErr("--percent must be between 0 and 100")
			}
			ctx := cmd.Context()
			job, err := a.prepareBuild(ctx, args[0], f)
			if err != nil {
				return err
			}
			job.opts.Percent, job.opts.Channel = percent, channel
			res, err := job.svc.Release(ctx, job.opts)
			if err != nil {
				return buildErr(err)
			}
			p := a.printer()
			noticeUnsigned(p, res)
			view := output.NewReleaseView(res)
			view.AlsoActive = alsoActive(ctx, p, job.svc, res.DeploymentID, res.Chip)
			return p.Release(view)
		},
	}
	f.register(cmd.Flags())
	cmd.Flags().IntVar(&percent, "percent", 100, "percentage of the channel's devices to offer the build to")
	cmd.Flags().StringVar(&channel, "channel", "stable", "release channel; it must already exist on the project")
	return cmd
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
