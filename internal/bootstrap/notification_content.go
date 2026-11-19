package bootstrap

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/notifier"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
)

func (n *credentialNotifications) notificationPayload(ctx context.Context, a credentialAlert, message notifier.Message) ([]byte, error) {
	store := &notificationStore{database: n.runtime.database}
	t, err := store.Template(ctx, a.TenantID, a.Template, "zh-CN")
	if err != nil {
		return nil, err
	}
	title := t.Name
	if title == "" {
		title = a.Template
	}
	body := ""
	if rendered, err := channel.Render(t, credentialNotificationVariables(a)); err == nil {
		body = rendered.Body
	}
	return json.Marshal(struct {
		notifier.Message
		Title string `json:"title"`
		Body  string `json:"body"`
	}{message, title, body})
}

func setInboxContent(row *credentialNotification) {
	var content struct {
		Title, Body              string
		Platform, Severity, Path string
	}
	if json.Unmarshal(row.Payload, &content) != nil {
		return
	}
	row.Title = content.Title
	if row.Title == "" {
		row.Title = row.TemplateName
	}
	body := content.Body
	if body == "" && row.TemplateBody != "" {
		t := channel.Template{Enabled: true, Format: row.TemplateFormat, Subject: "", Body: row.TemplateBody}
		variables := map[string]string{"Platform": content.Platform, "Severity": content.Severity, "Path": content.Path}
		if rendered, err := channel.Render(t, variables); err == nil {
			body = rendered.Body
		}
	}
	runes := []rune(body)
	if len(runes) > 120 {
		runes = runes[:120]
	}
	row.Summary = string(runes)
}
