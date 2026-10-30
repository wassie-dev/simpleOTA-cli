package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

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
			return a.printer().ChannelShow(output.NewChannelView(c))
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
			views := viewsOf(channels, output.NewChannelView)
			return a.printer().ChannelList(views)
		},
	})
	return cmd
}

// channelNames maps channel IDs to names.
func channelNames(channels []api.Channel) map[string]string {
	names := make(map[string]string, len(channels))
	for _, c := range channels {
		names[c.ID] = c.Name
	}
	return names
}

// matchChannelID finds a channel by name or ID in a listing.
func matchChannelID(channels []api.Channel, name string) (string, error) {
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
