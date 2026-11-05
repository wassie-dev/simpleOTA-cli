package cli

import (
	"github.com/spf13/cobra"

	"github.com/wassie-dev/simpleOTA-cli/internal/output"
)

func (a *App) auditCommand() *cobra.Command {
	var eventType string
	var limit int
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Show the project's audit log",
		Long: `Show the current project's audit log, newest first: who uploaded builds and
changed deployments, devices and signing keys, and when. Events the server
made itself, such as an automatic pause, show as 'system'.

Common --type values:
  artifact.uploaded
  deployment.created
  deployment.ramp
  deployment.auto_paused
  device.first_check_in
  signing_key.created
  signing_key.revoked`,
		Example: `  simpleota audit
  simpleota audit --type signing_key.revoked --json`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 200 {
				return usageErr("--limit must be between 1 and 200")
			}
			s, projectID, err := a.projectSession(cmd.Context())
			if err != nil {
				return err
			}
			events, err := s.api.ListAudit(cmd.Context(), projectID, eventType, limit)
			if err != nil {
				return err
			}
			views := viewsOf(events, output.NewAuditView)
			return a.printer().AuditList(views)
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "only events of this `type`")
	cmd.Flags().IntVar(&limit, "limit", 50, "show at most `n` events, up to 200")
	return cmd
}
