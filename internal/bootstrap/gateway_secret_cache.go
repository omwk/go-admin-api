package bootstrap

import "github.com/omwk/go-admin-api/internal/modules/gateway"

func cloneGatewaySecrets(keys []gateway.Secret) []gateway.Secret {
	out := append([]gateway.Secret{}, keys...)
	for i := range out {
		out[i].CipherText = append([]byte{}, keys[i].CipherText...)
		out[i].Nonce = append([]byte{}, keys[i].Nonce...)
		if keys[i].ExpiresAt != nil {
			expiry := *keys[i].ExpiresAt
			out[i].ExpiresAt = &expiry
		}
		if keys[i].LastUsedAt != nil {
			used := *keys[i].LastUsedAt
			out[i].LastUsedAt = &used
		}
	}
	return out
}
