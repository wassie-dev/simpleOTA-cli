package cli

import (
	"context"
	"errors"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) deviceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "device",
		Aliases: []string{"devices"},
		Short:   "List, inspect and update devices",
		Long: `A device is one board in the current project's fleet. Devices register
themselves the first time their firmware checks in, and report their build,
chip and compatibility on every check.

Name a device by the device ID its firmware reports, such as a MAC address,
or by its SimpleOTA UUID.`,
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
		Long: `List the current project's devices with their build, channel and state, and
when they were last seen.`,
		Example: `  simpleota device list
  simpleota device list --channel beta --state active
  simpleota device list --json | jq -r '.[].device_id'`,
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
				if f.ChannelID, err = matchChannelID(channels, channel); err != nil {
					return err
				}
			}
			if group != "" {
				if f.GroupID, err = findGroupID(ctx, s.api, projectID, group); err != nil {
					return err
				}
			}
			devices, err := s.api.ListDevices(ctx, projectID, f)
			if err != nil {
				return err
			}
			names := channelNames(channels)
			views := viewsOf(devices, func(d api.Device) output.DeviceView { return output.NewDeviceView(d, names) })
			return a.printer().DeviceList(views)
		},
	}
	list.Flags().StringVar(&channel, "channel", "", "only devices on the channel with this `name`")
	list.Flags().StringVar(&group, "group", "", "only devices in the group with this `name`")
	list.Flags().StringVar(&state, "state", "", "only devices in this `state`: active or inactive")

	show := &cobra.Command{
		Use:   "show <device-id|uuid>",
		Short: "Show one device",
		Long: `Show one device: its build, channel, last check-in and last OTA result, and
the compatibility it reports (framework, chip, board, hardware revision,
partition profile, NVS schema, security mode), which decides the builds it
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
		Long: `Show a device's most recent update checks, newest first: the build it was
running, any build it was offered, and the result. The API returns at most 20.

Results:
  offered               a build was offered
  up_to_date            already on the latest compatible build
  not_in_rollout        not yet inside the deployment's rollout percentage
  no_compatible_build   no build in an active deployment fits this device
  no_active_deployment  no active deployment targets this device
  device_inactive       updates are disabled for this device
  over_limit            the project is over its device limit`,
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
			views := viewsOf(events, output.NewCheckEventView)
			return a.printer().CheckEventList(views)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "show at most `n` checks, up to 20")
	return cmd
}

func (a *App) deviceUpdateCommand() *cobra.Command {
	var name, channel, state string
	cmd := &cobra.Command{
		Use:   "update <device-id|uuid>",
		Short: "Rename, move or (de)activate a device",
		Long: `Change a device's name, release channel or state. Only the flags you pass are
changed.

An inactive device is never offered builds and does not count toward the
plan's device limit. Activating one counts it again, and is refused when the
plan is full.`,
		Example: `  simpleota device update lamp-kitchen --channel beta
  simpleota device update lamp-kitchen --name "Kitchen lamp"
  simpleota device update lamp-kitchen --state inactive`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if !fl.Changed("name") && !fl.Changed("channel") && !fl.Changed("state") {
				return usageErr("nothing to change\nPass --name, --channel or --state.")
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
				id, err := matchChannelID(channels, channel)
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
	cmd.Flags().StringVar(&name, "name", "", "new display `name`")
	cmd.Flags().StringVar(&channel, "channel", "", "move the device to the channel with this `name`")
	cmd.Flags().StringVar(&state, "state", "", "new `state`: active or inactive")
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
	return api.Device{}, usageErr("no device %q in this project\nList devices with:\n  simpleota device list", ref)
}
