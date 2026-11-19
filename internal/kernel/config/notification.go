package config

// Notification optionally restricts personal destinations to exact public hosts.
type Notification struct {
	WebhookAllowedHosts []string `mapstructure:"webhook_allowed_hosts"`
	BarkAllowedHosts    []string `mapstructure:"bark_allowed_hosts"`
	GotifyAllowedHosts  []string `mapstructure:"gotify_allowed_hosts"`
}
