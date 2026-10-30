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
		Short:   "List and inspect firmware builds",
		Long: `A build is one uploaded firmware image in the current project, numbered
by the server in upload order. Devices are offered only builds higher than
the one they run. The API calls builds artifacts.

Builds are created by simpleota release, and are never edited or deleted.`,
		Example: `  simpleota build list
  simpleota build show 42`,
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
			number, err := strconv.Atoi(args[0])
			if err != nil || number < 1 {
				return usageErr("%q is not a build number; simpleota build list prints them", args[0])
			}
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			artifacts, err := s.api.ListArtifacts(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			for _, art := range artifacts {
				if art.BuildNumber == number {
					return a.printer().BuildShow(output.NewBuildView(art))
				}
			}
			return usageErr("this project has no build %d; simpleota build list prints them", number)
		},
	})
	return cmd
}

func (a *App) deviceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "device",
		Aliases: []string{"devices"},
		Short:   "List and inspect devices",
		Long: `A device is one board in the current project's fleet. Devices register
themselves the first time their firmware checks in, and report the build
they run, their chip and their compatibility on every check.

A device is named by its device ID (the ID its firmware reports, such as
a MAC address) or by its SimpleOTA UUID.`,
		Example: `  simpleota device list --channel beta
  simpleota device show lamp-kitchen`,
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
	cmd.AddCommand(list, show)
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
	for _, d := range devices {
		if d.DeviceID == ref {
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
		Short:   "List release channels",
		Long: `A release channel, such as stable or beta, is a named audience of
devices in the current project. Each device is on one channel, and a
deployment is offered to the devices on the channels it targets. Every
project starts with a stable channel; new devices join the default one.`,
		Example: `  simpleota channel list`,
		Args:    cobra.ArbitraryArgs,
		RunE:    groupRun,
	}
	cmd.AddCommand(&cobra.Command{
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
