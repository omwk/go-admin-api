package notifier

// InboxFilter narrows one authenticated user's notification list.
type InboxFilter struct {
	Unread   *bool
	Template string
	Channel  string
}

// Preference is an explicit override for one template and external channel.
type Preference struct {
	Template string `json:"template"`
	Channel  string `json:"channel"`
	Enabled  *bool  `json:"enabled"`
}

// PreferenceView includes the effective value and whether it is inherited.
type PreferenceView struct {
	Template      string `json:"template"`
	Name          string `json:"name"`
	Transactional bool   `json:"transactional"`
	Channel       string `json:"channel"`
	Enabled       bool   `json:"enabled"`
	Inherited     bool   `json:"inherited"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason"`
}

// ExternalChannels returns the supported personal delivery preference channels.
func ExternalChannels() []string { return []string{"email", "webhook", "bark", "gotify"} }
