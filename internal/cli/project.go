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
