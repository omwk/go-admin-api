// Package notifier delivers metadata-only credential maintenance alerts.
package notifier

import (
	"context"
	"errors"
)

// Message contains only the fields approved for external delivery.
type Message struct {
	EventID  int64  `json:"event_id"`
	Template string `json:"template"`
	Platform string `json:"platform"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
}

// Sender delivers a stable event ID for at-least-once receiver deduplication.
type Sender interface {
	Send(context.Context, Message) error
}

// Validate rejects unknown templates and arbitrary navigation URLs.
func (m Message) Validate() error {
	if m.EventID <= 0 || m.Path != "/admin/notifications" || len(m.Platform) > 40 {
		return errors.New("invalid maintenance notification")
	}
	switch m.Template {
	case "credential.expiring.3d", "credential.expiring.1d", "credential.expired", "credential.needs_reauth", "credential.unknown", "credential.failed", "credential.stuck", "credential.recovered", "credential.protocol_pending":
	default:
		return errors.New("invalid maintenance notification template")
	}
	switch m.Severity {
	case "info", "warning", "critical":
		return nil
	default:
		return errors.New("invalid maintenance notification severity")
	}
}
