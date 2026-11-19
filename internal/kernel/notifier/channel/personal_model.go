package channel

import (
	"fmt"
	"time"
)

// PersonalChannelInput carries a write-only URL and optional enablement.
type PersonalChannelInput struct {
	Endpoint string `json:"endpoint"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// PersonalChannelView exposes only masked destination metadata.
type PersonalChannelView struct {
	Channel        string     `json:"channel"`
	EndpointMasked string     `json:"endpoint_masked"`
	Enabled        bool       `json:"enabled"`
	VerifiedAt     *time.Time `json:"verified_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// PersonalChannelAAD binds a ciphertext to its tenant, owner and channel.
func PersonalChannelAAD(tenant, user int64, kind string) string {
	return fmt.Sprintf("notification-user-channel:v1:%d:%d:%s", tenant, user, kind)
}
