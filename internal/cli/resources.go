package cli

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

// projectSession opens a session and resolves the current project.
func (a *App) projectSession(ctx context.Context) (*session, string, error) {
	s, err := a.openSession()
	if err != nil {
		return nil, "", err
	}
	projectID, err := s.projectID(ctx)
	if err != nil {
		return nil, "", err
	}
	return s, projectID, nil
}

func (a *App) buildCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "build",
		Aliases: []string{"builds"},
		Short:   "List, upload and archive firmware builds",
		Long: `A build is one uploaded firmware image in the current project, numbered
by the server in upload order. Devices are offered only builds higher than
the one they run. The API calls builds artifacts.

simpleota release uploads a build and deploys it in one step; build upload
only uploads it, for simpleota deployment create to deploy later. Builds
are never edited or deleted. Archiving takes one out of service: it is no
longer offered, and cannot be deployed until it is unarchived.`,
		Example: `  simpleota build list
  simpleota build show 42
  simpleota build upload build/app.bin --version-label 1.4.0
  simpleota build archive 41`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the project's builds, newest first",
		Long: `List the current project's builds, newest first, with their version
label, chip, security mode and whether they are archived.`,
		Example: `  simpleota build list
  simpleota build list --json | jq '.[0].build_number'`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			artifacts, err := s.api.ListArtifacts(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			sort.SliceStable(artifacts, func(i, j int) bool { return artifacts[i].BuildNumber > artifacts[j].BuildNumber })
			views := make([]output.BuildView, len(artifacts))
			for i, art := range artifacts {
				views[i] = output.NewBuildView(art)
			}
			return a.printer().BuildList(views)
		},
	}, &cobra.Command{
		Use:   "show <build-number>",
		Short: "Show one build",
		Long: `Show a build's version label, compatibility (framework, chip, board,
partition profile, NVS schema), security mode, SHA-256, size and any
warnings the server recorded at upload.`,
		Example: `  simpleota build show 42`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			art, err := findBuild(cmd.Context(), s.api, projectID, args[0])
			if err != nil {
				return err
			}
			return a.printer().BuildShow(output.NewBuildView(art))
		},
	}, a.buildUploadCommand(), a.buildArchiveCommand(true), a.buildArchiveCommand(false))
	return cmd
}

func (a *App) buildUploadCommand() *cobra.Command {
	var f buildFlags
	cmd := &cobra.Command{
		Use:   "upload <bin> --version-label <label>",
		Short: "Sign and upload a build without deploying it",
		Long: `Upload an application binary to the current project as a new build,
without deploying it. Deploy it later with simpleota deployment create.

` + uploadHelp,
		Example: `  simpleota build upload build/app.bin --version-label 1.4.0
  simpleota deployment create --build 42 --percent 10`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			job, err := a.prepareBuild(cmd.Context(), args[0], f)
			if err != nil {
				return err
			}
			res, err := job.svc.Upload(cmd.Context(), job.opts)
			if err != nil {
				return buildErr(err)
			}
			p := a.printer()
			noticeUnsigned(p, res)
			return p.Upload(output.NewUploadView(res))
		},
	}
	f.register(cmd.Flags())
	return cmd
}

func (a *App) buildArchiveCommand(archive bool) *cobra.Command {
	use, short, long := "archive", "Take a build out of service", `Archive a build: devices are no longer offered it, and it cannot be
deployed. The record of which devices ran it is kept, and builds are
never deleted. The server refuses while a draft, active or paused
deployment still offers the build; complete or cancel it first.`
	if !archive {
		use, short, long = "unarchive", "Return an archived build to service", `Unarchive a build, so it can be offered and deployed again.`
	}
	return &cobra.Command{
		Use:     use + " <build-number>",
		Short:   short,
		Long:    long,
		Example: "  simpleota build " + use + " 41",
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			art, err := findBuild(ctx, s.api, projectID, args[0])
			if err != nil {
				return err
			}
			art, err = s.api.UpdateArtifact(ctx, projectID, art.ID, archive)
			if err != nil {
				return err
			}
			return a.printer().BuildShow(output.NewBuildView(art))
		},
	}
}

// findBuild looks a build up by its number.
func findBuild(ctx context.Context, client api.API, projectID, ref string) (api.Artifact, error) {
	number, err := strconv.Atoi(ref)
	if err != nil || number < 1 {
		return api.Artifact{}, usageErr("%q is not a build number; simpleota build list prints them", ref)
	}
	artifacts, err := client.ListArtifacts(ctx, projectID)
	if err != nil {
		return api.Artifact{}, err
	}
	for _, art := range artifacts {
		if art.BuildNumber == number {
			return art, nil
		}
	}
	return api.Artifact{}, usageErr("this project has no build %d; simpleota build list prints them", number)
}

func (a *App) deviceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "device",
		Aliases: []string{"devices"},
		Short:   "List, inspect and update devices",
		Long: `A device is one board in the current project's fleet. Devices register
themselves the first time their firmware checks in, and report the build
they run, their chip and their compatibility on every check.

A device is named by its device ID (the ID its firmware reports, such as
a MAC address) or by its SimpleOTA UUID.`,
		Example: `  simpleota device list --channel beta
  simpleota device show lamp-kitchen
  simpleota device events lamp-kitchen
  simpleota device update lamp-kitchen --channel beta`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	var channel, group, state string
	list := &cobra.Command{
		Use:   "list",
		Short: "List the project's devices",
		Long: `List the current project's devices with the build they run, their
release channel, whether they are checking in, and when they were last
seen. Filter by channel name, group name, or active state.`,
		Example: `  simpleota device list
  simpleota device list --channel beta --state active
  simpleota device list --json | jq -r '.[] | select(.current_build_number < 42) | .device_id'`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			var f api.DeviceFilter
			switch state {
			case "":
			case "active", "inactive":
				active := state == "active"
				f.Active = &active
			default:
				return usageErr("--state must be active or inactive")
			}
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			channels, err := s.api.ListChannels(ctx, projectID)
			if err != nil {
				return err
			}
			if channel != "" {
				if f.ChannelID, err = channelID(channels, channel); err != nil {
					return err
				}
			}
			if group != "" {
				if f.GroupID, err = groupID(ctx, s.api, projectID, group); err != nil {
					return err
				}
			}
			devices, err := s.api.ListDevices(ctx, projectID, f)
			if err != nil {
				return err
			}
			names := channelNames(channels)
			views := make([]output.DeviceView, len(devices))
			for i, d := range devices {
				views[i] = output.NewDeviceView(d, names)
			}
			return a.printer().DeviceList(views)
		},
	}
	list.Flags().StringVar(&channel, "channel", "", "only devices on this release channel")
	list.Flags().StringVar(&group, "group", "", "only devices in this group")
	list.Flags().StringVar(&state, "state", "", "only active or inactive devices")

	show := &cobra.Command{
		Use:   "show <device-id|uuid>",
		Short: "Show one device",
		Long: `Show a device's build, channel, last check-in and OTA result, and the
compatibility it reports (framework, chip, board, hardware revision,
partition profile, NVS schema, security mode), which decide the builds it
is offered.`,
		Example: `  simpleota device show lamp-kitchen
  simpleota device show 9b2f61d0-4c1e-4d5a-8f3e-2a6b7c8d9e0f --json`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			d, err := findDevice(ctx, s.api, projectID, args[0])
			if err != nil {
				return err
			}
			channels, err := s.api.ListChannels(ctx, projectID)
			if err != nil {
				return err
			}
			return a.printer().DeviceShow(output.NewDeviceView(d, channelNames(channels)))
		},
	}
	cmd.AddCommand(list, show, a.deviceEventsCommand(), a.deviceUpdateCommand())
	return cmd
}

func (a *App) deviceEventsCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "events <device-id|uuid>",
		Short: "Show a device's recent update checks",
		Long: `Show a device's most recent OTA checks, newest first: the build it ran,
the build it was offered if any, and why it was not offered one (up to
date, not in the rollout, no compatible build, no active deployment,
inactive, or over the plan's device limit). The API returns at most 20.`,
		Example: `  simpleota device events lamp-kitchen
  simpleota device events lamp-kitchen --limit 5 --json`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > 20 {
				return usageErr("--limit must be between 1 and 20")
			}
			ctx := cmd.Context()
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			d, err := findDevice(ctx, s.api, projectID, args[0])
			if err != nil {
				return err
			}
			events, err := s.api.ListCheckEvents(ctx, projectID, d.ID, limit)
			if err != nil {
				return err
			}
			views := make([]output.CheckEventView, len(events))
			for i, e := range events {
				views[i] = output.NewCheckEventView(e)
			}
			return a.printer().CheckEventList(views)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "how many checks to show, at most 20")
	return cmd
}

func (a *App) deviceUpdateCommand() *cobra.Command {
	var name, channel, state string
	cmd := &cobra.Command{
		Use:   "update <device-id|uuid>",
		Short: "Rename a device, move it to a channel, or (de)activate it",
		Long: `Change a device's name, release channel, or state. Only the flags you
pass are changed.

An inactive device is never offered builds and does not count toward the
plan's device limit. Activating one counts again, and is refused when the
plan is full.`,
		Example: `  simpleota device update lamp-kitchen --channel beta
  simpleota device update lamp-kitchen --name "Kitchen lamp"
  simpleota device update lamp-kitchen --state inactive`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if !fl.Changed("name") && !fl.Changed("channel") && !fl.Changed("state") {
				return usageErr("nothing to change; pass --name, --channel or --state")
			}
			var in api.DeviceUpdate
			if fl.Changed("name") {
				in.Name = &name
			}
			if fl.Changed("state") {
				if state != "active" && state != "inactive" {
					return usageErr("--state must be active or inactive")
				}
				active := state == "active"
				in.Active = &active
			}
			ctx := cmd.Context()
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			d, err := findDevice(ctx, s.api, projectID, args[0])
			if err != nil {
				return err
			}
			channels, err := s.api.ListChannels(ctx, projectID)
			if err != nil {
				return err
			}
			if fl.Changed("channel") {
				id, err := channelID(channels, channel)
				if err != nil {
					return err
				}
				in.ChannelID = &id
			}
			d, err = s.api.UpdateDevice(ctx, projectID, d.ID, in)
			if err != nil {
				return err
			}
			return a.printer().DeviceShow(output.NewDeviceView(d, channelNames(channels)))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&channel, "channel", "", "release channel to move the device to")
	cmd.Flags().StringVar(&state, "state", "", "active or inactive")
	return cmd
}

// findDevice accepts a device's SimpleOTA UUID or the device ID its
// firmware reports.
func findDevice(ctx context.Context, client api.API, projectID, ref string) (api.Device, error) {
	if api.IsUUID(ref) {
		d, err := client.GetDevice(ctx, projectID, ref)
		var ae *api.Error
		if err == nil || !errors.As(err, &ae) || ae.StatusCode != http.StatusNotFound {
			return d, err
		}
	}
	devices, err := client.ListDevices(ctx, projectID, api.DeviceFilter{})
	if err != nil {
		return api.Device{}, err
	}
	return matchDevice(devices, ref)
}

// matchDevice finds a device by UUID or reported device ID in a listing.
func matchDevice(devices []api.Device, ref string) (api.Device, error) {
	for _, d := range devices {
		if d.ID == ref || d.DeviceID == ref {
			return d, nil
		}
	}
	return api.Device{}, usageErr("no device %q in this project; simpleota device list prints them", ref)
}

func channelNames(channels []api.Channel) map[string]string {
	names := make(map[string]string, len(channels))
	for _, c := range channels {
		names[c.ID] = c.Name
	}
	return names
}

func channelID(channels []api.Channel, name string) (string, error) {
	for _, c := range channels {
		if c.Name == name || c.ID == name {
			return c.ID, nil
		}
	}
	names := make([]string, len(channels))
	for i, c := range channels {
		names[i] = c.Name
	}
	return "", usageErr("no channel %q in this project (channels: %s)", name, strings.Join(names, ", "))
}

func groupID(ctx context.Context, client api.API, projectID, name string) (string, error) {
	groups, err := client.ListGroups(ctx, projectID)
	if err != nil {
		return "", err
	}
	names := make([]string, len(groups))
	for i, g := range groups {
		if g.Name == name || g.ID == name {
			return g.ID, nil
		}
		names[i] = g.Name
	}
	if len(names) == 0 {
		return "", usageErr("no group %q: this project has no device groups", name)
	}
	return "", usageErr("no group %q in this project (groups: %s)", name, strings.Join(names, ", "))
}

func (a *App) channelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "channel",
		Aliases: []string{"channels"},
		Short:   "List and create release channels",
		Long: `A release channel, such as stable or beta, is a named audience of
devices in the current project. Each device is on one channel, and a
deployment is offered to the devices on the channels it targets. Every
project starts with a stable channel; new devices join the default one.`,
		Example: `  simpleota channel list
  simpleota channel create beta`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	var description string
	var isDefault bool
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a release channel",
		Long: `Create a release channel in the current project. Move devices onto it with
simpleota device update --channel, and target it with
simpleota release --channel or simpleota deployment create --channel.
--default makes it the channel new devices join, in place of the current
default.`,
		Example: `  simpleota channel create beta
  simpleota channel create canary --description "Office devices" --default`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return usageErr("the channel name must not be empty")
			}
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			c, err := s.api.CreateChannel(cmd.Context(), projectID, api.ChannelRequest{Name: name, Description: description, IsDefault: isDefault})
			if err != nil {
				return err
			}
			return a.printer().ChannelShow(output.ChannelView{ID: c.ID, Name: c.Name, Description: c.Description, IsDefault: c.IsDefault})
		},
	}
	create.Flags().StringVar(&description, "description", "", "what the channel is for")
	create.Flags().BoolVar(&isDefault, "default", false, "make it the channel new devices join")
	cmd.AddCommand(create, &cobra.Command{
		Use:     "list",
		Short:   "List the project's release channels",
		Long:    `List the current project's release channels, marking the default one.`,
		Example: `  simpleota channel list`,
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			channels, err := s.api.ListChannels(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			views := make([]output.ChannelView, len(channels))
			for i, c := range channels {
				views[i] = output.ChannelView{ID: c.ID, Name: c.Name, Description: c.Description, IsDefault: c.IsDefault}
			}
			return a.printer().ChannelList(views)
		},
	})
	return cmd
}
