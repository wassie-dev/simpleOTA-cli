package output

import (
	"fmt"
	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AuditView is one audit log entry.
type AuditView struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	EventType  string         `json:"event_type"`
	ActorID    *int           `json:"actor_id"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Payload    map[string]any `json:"payload"`
}

// NewAuditView converts an API audit event.
func NewAuditView(e api.AuditEvent) AuditView {
	return AuditView{ID: e.ID, At: e.CreatedAt, EventType: e.EventType, ActorID: e.ActorID, TargetType: e.TargetType, TargetID: e.TargetID, Payload: e.Payload}
}

// AuditList prints audit.
func (p Printer) AuditList(events []AuditView) error {
	if p.JSON {
		return p.WriteJSON(events)
	}
	if len(events) == 0 {
		fmt.Fprintln(p.Out, "No audit events.")
		return nil
	}
	rows := make([][]string, len(events))
	for i, e := range events {
		actor := "system"
		if e.ActorID != nil {
			actor = "user " + strconv.Itoa(*e.ActorID)
		}
		rows[i] = []string{when(e.At), e.EventType, actor, e.TargetType + " " + e.TargetID, payloadText(e.Payload)}
	}
	return p.table([]string{"TIME", "EVENT", "BY", "TARGET", "DETAILS"}, rows)
}

// payloadText renders an event payload as sorted key=value pairs.
func payloadText(payload map[string]any) string {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, payload[k])
	}
	return strings.Join(parts, " ")
}
