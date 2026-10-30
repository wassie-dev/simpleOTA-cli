package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// findProject accepts a project UUID or slug.
func findProject(ctx context.Context, a api.API, ref string) (api.Project, error) {
	if api.IsUUID(ref) {
		p, err := a.GetProject(ctx, ref)
		var ae *api.Error
		if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
			return p, usageErr("project %s does not exist or this token cannot see it", ref)
		}
		return p, err
	}
	projects, err := a.ListProjects(ctx)
	if isStaffListing(err) {
		return api.Project{}, usageErr("a staff account selects projects by ID, not slug; pass --project <project-id>, copying the Project ID from the project's page")
	}
	if err != nil {
		return api.Project{}, err
	}
	for _, p := range projects {
		if p.Slug == ref {
			return p, nil
		}
	}
	return api.Project{}, usageErr("no project with slug %q is visible to this token (visible: %s)", ref, projectSlugs(projects))
}

func projectSlugs(ps []api.Project) string {
	if len(ps) == 0 {
		return "none"
	}
	slugs := make([]string, len(ps))
	for i, p := range ps {
		slugs[i] = p.Slug
	}
	return strings.Join(slugs, ", ")
}

// isStaffListing reports the 400 the API gives a staff token that lists
// projects without naming an owner.
func isStaffListing(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.StatusCode == http.StatusBadRequest
}
