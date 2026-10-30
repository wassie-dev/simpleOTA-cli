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
		Long: `Show the current project's audit log, newest first: who uploaded builds,
changed deployments, devices and signing keys, and when. An event made by
the server itself, such as an automatic pause, shows as system.

Filter with --type, for example artifact.uploaded, deployment.created,
deployment.ramp, deployment.auto_paused, device.first_check_in,
signing_key.created or signing_key.revoked.`,
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
			views := make([]output.AuditView, len(events))
			for i, e := range events {
				views[i] = output.AuditView{ID: e.ID, At: e.CreatedAt, EventType: e.EventType, ActorID: e.ActorID, TargetType: e.TargetType, TargetID: e.TargetID, Payload: e.Payload}
			}
			return a.printer().AuditList(views)
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "only events of this type")
	cmd.Flags().IntVar(&limit, "limit", 50, "how many events to show, at most 200")
	return cmd
}
