package notifier

import (
	"errors"
	"net"
	"net/mail"
	"net/url"
	"strings"
)

// Settings holds delivery configuration. Persistence must encrypt the whole value.
type Settings struct {
	EmailEnabled   bool   `json:"email_enabled"`
	WebhookEnabled bool   `json:"webhook_enabled"`
	SMTPAddr       string `json:"smtp_addr"`
	SMTPUser       string `json:"smtp_user"`
	SMTPPassword   string `json:"smtp_password"`
	SMTPFrom       string `json:"smtp_from"`
	WebhookURL     string `json:"webhook_url"`
	WebhookSecret  string `json:"webhook_secret"`
}

// SettingsView reports channel availability without exposing destinations or secrets.
type SettingsView struct {
	EmailEnabled   bool `json:"email_enabled"`
	WebhookEnabled bool `json:"webhook_enabled"`
}

// View returns only safe status flags.
func (s Settings) View() SettingsView { return SettingsView{s.EmailEnabled, s.WebhookEnabled} }

// Validate rejects malformed transport configuration before storing it.
func (s Settings) Validate() error {
	if len(s.SMTPPassword) > 4096 || len(s.WebhookSecret) > 4096 {
		return errors.New("notification configuration too large")
	}
	if s.EmailEnabled {
		host, port, err := net.SplitHostPort(s.SMTPAddr)
		if err != nil || host == "" || port == "" || strings.ContainsAny(s.SMTPAddr+s.SMTPUser, "\r\n") {
			return errors.New("invalid notification SMTP endpoint")
		}
		address, err := mail.ParseAddress(s.SMTPFrom)
		if err != nil || address.Address != s.SMTPFrom || strings.ContainsAny(s.SMTPFrom, "\r\n") {
			return errors.New("invalid notification sender")
		}
	}
	if s.WebhookEnabled {
		u, err := url.Parse(s.WebhookURL)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || len(s.WebhookURL) > 2048 {
			return errors.New("invalid notification webhook")
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
			return errors.New("notification webhook requires HTTPS")
		}
	}
	return nil
}
