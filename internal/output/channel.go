package output

import (
	"fmt"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// ChannelView is a release channel.
type ChannelView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDefault   bool   `json:"is_default"`
}

// NewChannelView converts an API channel.
func NewChannelView(c api.Channel) ChannelView {
	return ChannelView{ID: c.ID, Name: c.Name, Description: c.Description, IsDefault: c.IsDefault}
}

// ChannelShow prints one channel, as channel create does.
func (p Printer) ChannelShow(c ChannelView) error {
	if p.JSON {
		return p.WriteJSON(c)
	}
	fmt.Fprintf(p.Out, "Channel %s\n", p.bold(c.Name))
	p.field("id", c.ID)
	if c.IsDefault {
		p.field("default", "yes; new devices join it")
	}
	p.field("description", c.Description)
	return nil
}

// ChannelList prints channel list.
func (p Printer) ChannelList(channels []ChannelView) error {
	if p.JSON {
		return p.WriteJSON(channels)
	}
	rows := make([][]string, len(channels))
	for i, c := range channels {
		def := ""
		if c.IsDefault {
			def = "yes"
		}
		rows[i] = []string{c.Name, def, c.Description, c.ID}
	}
	return p.table([]string{"NAME", "DEFAULT", "DESCRIPTION", "ID"}, rows)
}
