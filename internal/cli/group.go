package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) groupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "group",
		Aliases: []string{"groups"},
		Short:   "List and manage device groups",
		Long: `A device group is a named set of devices in the current project, such as the
boards in one building. A device is on one channel, but can be in any number
of groups.

To roll a build out to a group:
  simpleota deployment create --build <n> --group <name>

Name a group by its name or UUID. Name a device by the device ID its firmware
reports, or its SimpleOTA UUID.`,
		Example: `  simpleota group create kitchen
  simpleota group member add kitchen lamp-1 lamp-2
  simpleota group member list kitchen
  simpleota deployment create --build 42 --group kitchen`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	member := &cobra.Command{
		Use:     "member",
		Aliases: []string{"members"},
		Short:   "List, add and remove a group's devices",
		Long: `List a group's devices, or add and remove them. Name a device by the device
ID its firmware reports, or its SimpleOTA UUID.`,
		Example: `  simpleota group member list kitchen
  simpleota group member add kitchen lamp-1 lamp-2
  simpleota group member remove kitchen lamp-2`,
		Args: cobra.ArbitraryArgs,
		RunE: groupRun,
	}
	member.AddCommand(a.groupMemberListCommand(), a.groupMemberAddCommand(), a.groupMemberRemoveCommand())
	cmd.AddCommand(a.groupListCommand(), a.groupCreateCommand(), member)
	return cmd
}

func (a *App) groupListCommand() *cobra.Command {
	return withLimit(&cobra.Command{
		Use:     "list",
		Short:   "List the project's device groups",
		Long:    `List the current project's device groups.`,
		Example: `  simpleota group list`,
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			groups, err := s.api.ListGroups(cmd.Context(), projectID)
			if err != nil {
				return err
			}
			views := viewsOf(groups, output.NewGroupView)
			return printList(a, cmd, views, a.printer().GroupList)
		},
	})
}

func (a *App) groupCreateCommand() *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a device group",
		Long: `Create an empty device group in the current project.

Add devices with:
  simpleota group member add <group> <device>...`,
		Example: `  simpleota group create kitchen --description "Kitchen lights"`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return usageErr("the group name must not be empty")
			}
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			g, err := s.api.CreateGroup(cmd.Context(), projectID, api.GroupRequest{Name: name, Description: description})
			if err != nil {
				return err
			}
			return a.printer().GroupShow(output.NewGroupView(g))
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "short `text` saying what the group is for")
	return cmd
}

func (a *App) groupMemberListCommand() *cobra.Command {
	return withLimit(&cobra.Command{
		Use:   "list <group>",
		Short: "List a group's devices",
		Long: `List the devices in a group, most recently seen first, in the same form as
'simpleota device list'.`,
		Example: `  simpleota group member list kitchen`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, projectID, err := a.projectSession(ctx)
			if err != nil {
				return err
			}
			gid, err := findGroupID(ctx, s.api, projectID, args[0])
			if err != nil {
				return err
			}
			devices, err := s.api.ListGroupMembers(ctx, projectID, gid)
			if err != nil {
				return err
			}
			channels, err := s.api.ListChannels(ctx, projectID)
			if err != nil {
				return err
			}
			return a.printDevices(cmd, devices, channels)
		},
	})
}

func (a *App) groupMemberAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add <group> <device>...",
		Short: "Add devices to a group",
		Long: `Add one or more devices to a group. Every device is looked up first, so if
one is unknown, none are added.`,
		Example: `  simpleota group member add kitchen lamp-1 lamp-2`,
		Args:    minArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.changeMembers(cmd.Context(), args[0], args[1:], true)
		},
	}
}

func (a *App) groupMemberRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <group> <device>...",
		Short: "Remove devices from a group",
		Long: `Remove one or more devices from a group. They stay in the project and in
their other groups.`,
		Example: `  simpleota group member remove kitchen lamp-2`,
		Args:    minArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.changeMembers(cmd.Context(), args[0], args[1:], false)
		},
	}
}

// changeMembers adds or removes devices, resolving every reference before
// changing anything.
func (a *App) changeMembers(ctx context.Context, group string, refs []string, add bool) error {
	s, projectID, err := a.projectSession(ctx)
	if err != nil {
		return err
	}
	gid, err := findGroupID(ctx, s.api, projectID, group)
	if err != nil {
		return err
	}
	devices, err := s.api.ListDevices(ctx, projectID, api.DeviceFilter{})
	if err != nil {
		return err
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		d, err := matchDevice(devices, ref)
		if err != nil {
			return err
		}
		ids[i] = d.ID
	}
	if add {
		err = s.api.AddGroupMembers(ctx, projectID, gid, ids)
	} else {
		for _, id := range ids {
			if err = s.api.RemoveGroupMember(ctx, projectID, gid, id); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	return a.printer().MembersChanged(output.MembersView{Group: group, Devices: refs, Added: add})
}

// findGroupID looks a device group up by name or ID.
func findGroupID(ctx context.Context, client api.API, projectID, name string) (string, error) {
	groups, err := client.ListGroups(ctx, projectID)
	if err != nil {
		return "", err
	}
	return matchGroupID(groups, name)
}

// matchGroupID finds a device group by name or ID in a listing.
func matchGroupID(groups []api.Group, name string) (string, error) {
	names := make([]string, len(groups))
	for i, g := range groups {
		if g.Name == name || g.ID == name {
			return g.ID, nil
		}
		names[i] = g.Name
	}
	if len(names) == 0 {
		return "", usageErr("no group %q: this project has no device groups\nCreate one with:\n  simpleota group create <name>", name)
	}
	return "", usageErr("no group %q in this project\nGroups: %s", name, strings.Join(names, ", "))
}
