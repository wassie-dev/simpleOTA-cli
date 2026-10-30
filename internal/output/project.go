package output

import (
	"fmt"
	"strconv"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// ProjectView is a project. Fields other than id, slug and name are empty
// where the command did not fetch them.
type ProjectView struct {
	ID               string     `json:"id"`
	Slug             string     `json:"slug"`
	Name             string     `json:"name"`
	Description      string     `json:"description,omitempty"`
	DefaultFramework string     `json:"default_framework,omitempty"`
	BuildCounter     int        `json:"build_counter,omitempty"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
	// Current is set in project list on the project commands act on.
	Current bool `json:"current,omitempty"`
}

// NewProjectView converts an API project.
func NewProjectView(p api.Project) ProjectView {
	v := ProjectView{ID: p.ID, Slug: p.Slug, Name: p.Name, Description: p.Description, DefaultFramework: p.DefaultFramework, BuildCounter: p.BuildCounter}
	if !p.CreatedAt.IsZero() {
		t := p.CreatedAt
		v.CreatedAt = &t
	}
	return v
}

// ProjectList prints project list.
func (p Printer) ProjectList(projects []ProjectView) error {
	if p.JSON {
		return p.WriteJSON(projects)
	}
	if len(projects) == 0 {
		fmt.Fprintln(p.Out, "No projects. Create one in the dashboard.")
		return nil
	}
	rows := make([][]string, len(projects))
	for i, pr := range projects {
		mark := " "
		if pr.Current {
			mark = "*"
		}
		rows[i] = []string{mark, pr.Slug, pr.Name, pr.ID}
	}
	return p.table([]string{"", "SLUG", "NAME", "ID"}, rows)
}

// ProjectUseView is the result of project use.
type ProjectUseView struct {
	APIURL  string      `json:"api_url"`
	Project ProjectView `json:"project"`
}

// ProjectUse prints the result of project use.
func (p Printer) ProjectUse(v ProjectUseView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	fmt.Fprintf(p.Out, "Commands against %s now act on %s (%s).\n", v.APIURL, v.Project.Slug, v.Project.Name)
	return nil
}

// ProjectShow prints project show.
func (p Printer) ProjectShow(v ProjectView) error {
	if p.JSON {
		return p.WriteJSON(v)
	}
	fmt.Fprintf(p.Out, "Project %s\n", p.bold(v.Slug))
	p.field("name", v.Name)
	p.field("id", v.ID)
	p.field("framework", v.DefaultFramework)
	if v.BuildCounter > 0 {
		p.field("last build", strconv.Itoa(v.BuildCounter))
	}
	p.field("created", whenPtr(v.CreatedAt))
	p.field("description", v.Description)
	return nil
}
