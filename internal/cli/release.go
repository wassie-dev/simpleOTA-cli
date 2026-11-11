package cli

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/deploy"
	"github.com/wassie-dev/simpleOTA-cli/internal/manifest"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
	"github.com/wassie-dev/simpleOTA-cli/internal/release"
	"github.com/wassie-dev/simpleOTA-cli/internal/signing"
	"github.com/wassie-dev/simpleOTA-cli/internal/termtext"
)

const signingKeyEnv = "SIMPLEOTA_SIGNING_KEY"

// compatibleHelp describes the device count that release and deployment
// create report.
const compatibleHelp = `Compatible devices:
  The result gives the server's count of active devices in the deployment's
  audience whose last reported metadata (framework, chip, board, hardware
  revision, partitions, NVS schema, security mode) is compatible with the
  build. It does not apply the rollout percentage, and a device's next
  check-in can change it. List the devices with:
    simpleota deployment devices <deployment-id>
  With --require-devices, a count of 0 exits 4 and the deployment stays
  active. A server too old to report the count makes it exit 1 instead.`

// uploadHelp describes how release and build upload check, sign and send a
// build.
const uploadHelp = `Binary:
  The application image, not the bootloader, partition table, merged or
  filesystem image. Only the file name and the header's first byte and chip
  are checked.

Chip:
  Read from the image header. A --chip that differs is refused unless a
  device in the project reports that chip, because no other device would
  be offered the build.

Signing:
  Signed with the first key found, or left unsigned if there is none:
    1. --key-file
    2. SIMPLEOTA_SIGNING_KEY, an exported key (set
       SIMPLEOTA_SIGNING_KEY_PASSPHRASE if it is encrypted)
    3. the default key from 'simpleota key generate'

Upload:
  Sent once and never retried, because every accepted upload uses a build
  number and counts toward the plan's monthly limit. The server rejects it
  if the bytes it received do not match the binary's SHA-256.

  If the project already has this binary, the command stops before
  uploading, so it is safe to run again after a failed upload. Use
  --allow-duplicate to upload it anyway.`

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
	fl.StringVar(&f.versionLabel, "version-label", "", "version `label`, e.g. 1.4.0 (required)")
	fl.StringVar(&f.chip, "chip", "", "chip family `name`, e.g. esp32s3 (default: read from the image)")
	fl.StringVar(&f.framework, "framework", "", "framework `name`: arduino or esp_idf (default: the project's)")
	fl.StringVar(&f.keyFile, "key-file", "", "`path` of the signing key file")
	fl.StringVar(&f.securityMode, "security-mode", "auto", "signing `mode`: auto (sign if a key is available), basic or signed")
	fl.BoolVar(&f.allowDuplicate, "allow-duplicate", false, "upload even if the project has this binary")
}

func (f buildFlags) validate() error {
	if f.versionLabel == "" {
		return usageErr("--version-label is required, e.g. --version-label 1.4.0")
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
	session   *session
	projectID string
	svc       release.Service
	opts      release.Options
}

// prepareBuild validates the flags and resolves the current project. The
// signing key is loaded once the binary has passed its checks.
func (a *App) prepareBuild(ctx context.Context, binPath string, f buildFlags) (buildJob, error) {
	if err := f.validate(); err != nil {
		return buildJob{}, err
	}
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return buildJob{}, err
	}
	loadKey := func() (ed25519.PrivateKey, error) {
		if f.securityMode == manifest.SecurityBasic {
			return nil, nil
		}
		key, err := a.loadSigningKey(f.keyFile, s.cfg.SigningKeyFile)
		if err == nil && key == nil && f.securityMode == manifest.SecuritySigned {
			return nil, usageErr("--security-mode signed needs a key\nPass --key-file, or set %s.", signingKeyEnv)
		}
		return key, err
	}
	return buildJob{
		session:   s,
		projectID: projectID,
		svc:       release.Service{API: s.api, ProjectID: projectID},
		opts: release.Options{
			BinaryPath:     binPath,
			VersionLabel:   f.versionLabel,
			Chip:           f.chip,
			Framework:      f.framework,
			LoadKey:        loadKey,
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
		p.Notice("Uploaded unsigned (basic). Devices that enforce signed firmware will not be\noffered this build.")
	}
}

// requireDevicesFlag registers --require-devices.
func requireDevicesFlag(cmd *cobra.Command, require *bool) {
	cmd.Flags().BoolVar(require, "require-devices", false, "exit 4 if no active device in the audience has compatible metadata")
}

// errNoCount is a deployment response without
// compatible_audience_device_count, from a server older than the CLI.
var errNoCount = errors.New("the server did not report compatible_audience_device_count; it may be older than this CLI")

// compatibleCount is the server's count from a deployment response.
func compatibleCount(d api.Deployment) (*int, error) {
	if d.CompatibleAudienceDeviceCount == nil {
		return nil, errNoCount
	}
	return d.CompatibleAudienceDeviceCount, nil
}

// requireErr is what --require-devices does with the count. Without the flag
// a missing count is only a notice, printed before the result.
func requireErr(p output.Printer, require bool, deploymentID string, n *int, err error) error {
	id := termtext.Clean(deploymentID)
	switch {
	case !require:
		if err != nil {
			p.Notice("Could not get the number of compatible devices: %v", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("--require-devices could not check deployment %s: %w\nThe deployment is active. Check it with:\n  simpleota deployment show %s", id, err, id)
	case *n == 0:
		return &exitError{code: ExitNoDevices, err: fmt.Errorf("deployment %s has no compatible device\nNo active device in its audience has reported metadata compatible with the\nbuild. The deployment stays active, and a device can become compatible at\nits next check-in. Cancel it with:\n  simpleota deployment cancel %s", id, id)}
	}
	return nil
}

// alsoActive lists the other active deployments reaching the same devices,
// printing a notice instead of failing when the check itself fails.
func alsoActive(ctx context.Context, p output.Printer, s *session, projectID, deploymentID, chip string) []output.DeploymentView {
	others, err := deploy.AlsoActive(ctx, s.api, projectID, deploymentID, chip)
	if err != nil {
		p.Notice("Checking for other active deployments failed: %v", err)
	}
	return viewsOf(others, output.NewDeploymentView)
}

func (a *App) releaseCommand() *cobra.Command {
	var f buildFlags
	var percent int
	var channel string
	var requireDevices bool
	cmd := &cobra.Command{
		Use:   "release <bin> --version-label <label>",
		Short: "Sign, upload and deploy a firmware binary in one step",
		Long: `Upload a firmware binary to the current project and deploy it to a
channel, in one step. To upload now and deploy later, use
'simpleota build upload'.

The server assigns the build number. Devices are offered only builds higher
than the one they run.

If another deployment for the same chip is still active on the channel, it
is listed. A device installs the highest build offered to it, so the older
deployment keeps reaching the devices this one does not.

` + compatibleHelp + `

` + uploadHelp,
		Example: `  simpleota release build/app.bin --version-label 1.4.0
  simpleota release build/app.bin --version-label 1.4.1 --percent 10
  simpleota release build/app.bin --version-label 1.4.2 --channel beta

  # In CI: fail the job if no active device on the channel has compatible
  # metadata
  simpleota release build/app.bin --version-label 1.4.3 --require-devices`,
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
			res, err := job.svc.Release(ctx, job.opts, release.Target{Channel: channel, Percent: percent})
			if err != nil {
				return buildErr(err)
			}
			p := a.printer()
			noticeUnsigned(p, res)
			view := output.NewReleaseView(res)
			view.AlsoActive = alsoActive(ctx, p, job.session, job.projectID, res.DeploymentID, res.Chip)
			// The release response has no count, so read it from the
			// deployment.
			d, countErr := job.session.api.GetDeployment(ctx, job.projectID, res.DeploymentID)
			if countErr == nil {
				view.CompatibleDevices, countErr = compatibleCount(d)
			}
			reqErr := requireErr(p, requireDevices, res.DeploymentID, view.CompatibleDevices, countErr)
			if err := p.Release(view); err != nil {
				return err
			}
			return reqErr
		},
	}
	f.register(cmd.Flags())
	requireDevicesFlag(cmd, &requireDevices)
	cmd.Flags().IntVar(&percent, "percent", 100, "offer the build to `n` percent of the channel's devices")
	cmd.Flags().StringVar(&channel, "channel", api.DefaultChannel, "`name` of the channel to deploy to; it must already exist")
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
