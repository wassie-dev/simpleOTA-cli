package output

import (
	"fmt"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"strings"
)

// GroupView is a device group.
type GroupView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// NewGroupView converts an API device group.
func NewGroupView(g api.Group) GroupView {
	return GroupView{ID: g.ID, Name: g.Name, Description: g.Description}
}

// GroupList prints group list.
func (p Printer) GroupList(groups []GroupView) error {
	if p.JSON {
		return p.WriteJSON(groups)
	}
	if len(groups) == 0 {
		fmt.Fprintln(p.Out, "No device groups. Create one with: simpleota group create <name>")
		return nil
	}
	rows := make([][]string, len(groups))
	for i, g := range groups {
		rows[i] = []string{g.Name, g.Description, g.ID}
	}
	return p.table([]string{"NAME", "DESCRIPTION", "ID"}, rows)
}

// GroupShow prints one group, as group create does.
func (p Printer) GroupShow(g GroupView) error {
	if p.JSON {
		return p.WriteJSON(g)
	}
	fmt.Fprintf(p.Out, "Group %s\n", p.bold(g.Name))
	p.field("id", g.ID)
	p.field("description", g.Description)
	return nil
}

// MembersView is the result of group member add and remove.
type MembersView struct {
	Group   string   `json:"group"`
	Devices []string `json:"devices"`
	Added   bool     `json:"added"`
}

// MembersChanged prints the result of group member add and remove.
func (p Printer) MembersChanged(v MembersView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	verb, prep := "Added", "to"
	if !v.Added {
		verb, prep = "Removed", "from"
	}
	fmt.Fprintf(p.Out, "%s %s %s group %s.\n", verb, strings.Join(v.Devices, ", "), prep, v.Group)
	return nil
}
