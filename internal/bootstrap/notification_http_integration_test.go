package bootstrap

import (
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

// F3 §5.2/7/9: management sends through local SMTP, redacts credentials and enforces tenant limits.
func TestAdminNotificationHTTPIntegrationSMTPRedactionRateAndIdempotency(t *testing.T) {
	f := newNotificationFixtureF3(t)
	addr, calls := localSMTPF3(t)
	in := channel.ProviderInput{Channel: "email", Kind: "smtp", Name: "SMTP", Enabled: true, Config: channel.Config{"smtp_addr": addr, "from_address": f.email}}
	w := f.request(t, http.MethodPost, "/providers", "create", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	var p channel.ProviderView
	notificationDataF3(t, w, &p)
	require.NotContains(t, w.Body.String(), f.email)
	w = f.request(t, http.MethodPost, "/providers", "create", in)
	require.Equal(t, 201, w.Code)
	var replay channel.ProviderView
	notificationDataF3(t, w, &replay)
	require.Equal(t, p.ID, replay.ID)
	in.Priority = 2
	w = f.request(t, http.MethodPost, "/providers", "create", in)
	require.Equal(t, 409, w.Code)
	w = f.request(t, http.MethodPut, "/providers/"+fmtNotificationID(p.ID), "", in)
	require.Equal(t, 200, w.Code, w.Body.String())
	for i := 0; i < 3; i++ {
		key := "test-" + fmtNotificationID(int64(i))
		w = f.request(t, http.MethodPost, "/providers/"+fmtNotificationID(p.ID)+"/test", key, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var d channel.Delivery
		notificationDataF3(t, w, &d)
		require.Equal(t, "sent", d.Status)
		require.Equal(t, 1, d.Attempts)
		require.Nil(t, d.NextRetryAt)
	}
	w = f.request(t, http.MethodPost, "/providers/"+fmtNotificationID(p.ID)+"/test", "test-0", nil)
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 3, calls.Load())
	w = f.request(t, http.MethodPost, "/providers/"+fmtNotificationID(p.ID)+"/test", "fourth", nil)
	require.Equal(t, 429, w.Code, w.Body.String())
	w = f.request(t, http.MethodGet, "/providers", "", nil)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "from_address_configured")
	require.NotContains(t, w.Body.String(), f.email)
	w = f.request(t, http.MethodGet, "/deliveries", "", nil)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), f.email)
	w = f.request(t, http.MethodGet, "/templates", "", nil)
	require.Equal(t, 200, w.Code)
	in.Config = nil
	in.Enabled = false
	w = f.request(t, http.MethodPut, "/providers/"+fmtNotificationID(p.ID), "", in)
	require.Equal(t, 200, w.Code, w.Body.String())
	d := f.send(t, "email")
	require.Equal(t, "skipped", d.Status)
	require.EqualValues(t, 3, calls.Load())
	f.assertPrivacy(t, f.token)
}

func TestAdminNotificationHTTPIntegrationTemplateRetryDeleteAndAuthentication(t *testing.T) {
	f := newNotificationFixtureF3(t)
	template := channel.Template{Locale: "zh-CN", Subject: "Hello", Body: "{{.Name}}", Format: "text", Enabled: true}
	w := f.request(t, http.MethodPut, "/templates/notification.default", "template", template)
	require.Equal(t, 200, w.Code, w.Body.String())
	template.Body = "{{.Unknown}}"
	w = f.request(t, http.MethodPut, "/templates/invalid", "", template)
	require.Equal(t, 400, w.Code)
	d := f.send(t, "webhook")
	require.Equal(t, "skipped", d.Status)
	server, calls := mockAcceptedF3(t)
	p := f.provider(t, "http", "HTTP", 0, true, channel.Config{"endpoint": server.URL + "?key=" + f.token, "secret": f.token})
	w = f.request(t, http.MethodPost, "/deliveries/"+fmtNotificationID(d.ID)+"/retry", "retry", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var retried channel.Delivery
	notificationDataF3(t, w, &retried)
	require.Equal(t, "sent", retried.Status)
	w = f.request(t, http.MethodPost, "/deliveries/"+fmtNotificationID(d.ID)+"/retry", "retry", nil)
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 1, calls.Load())
	w = f.request(t, http.MethodPost, "/deliveries/"+fmtNotificationID(d.ID)+"/retry", "", nil)
	require.Equal(t, 409, w.Code)
	w = f.request(t, http.MethodGet, "/providers", "", nil)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), f.token)
	other := f.provider(t, "http", "unused", 0, false, channel.Config{"endpoint": server.URL, "secret": f.token})
	w = f.request(t, http.MethodDelete, "/providers/"+fmtNotificationID(other.ID), "delete", nil)
	require.Equal(t, 204, w.Code, w.Body.String())
	w = f.request(t, http.MethodDelete, "/providers/"+fmtNotificationID(other.ID), "delete", nil)
	require.Equal(t, 204, w.Code)
	_, err := f.manager.provider(f.ctx, channel.Actor{TenantID: 2, UserID: f.user.ID, Super: true}, p.ID, false)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
	f.token = ""
	w = f.request(t, http.MethodGet, "/providers", "", nil)
	require.Equal(t, 401, w.Code)
	f.assertPrivacy(t)
}
