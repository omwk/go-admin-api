package shop

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

func TestCredentialPublicExtraRejectsUnapprovedMetadata(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"scope":{}}`, `{"scope":null}`, `{"scope":1}`, `{"user_agent":""}`, `{"cookie":{}}`} {
		t.Run(raw, func(t *testing.T) {
			_, err := publicExtra(json.RawMessage(raw))
			require.Error(t, err)
		})
	}
	view, err := projectCredential(repo.CredentialRow{Credential: model.Credential{Extra: json.RawMessage(`{"scope":"order","unknown_private_field":"discarded"}`)}})
	require.NoError(t, err)
	encoded, err := json.Marshal(view.Extra)
	require.NoError(t, err)
	require.JSONEq(t, `{"scope":"order"}`, string(encoded))
}

func TestCredentialValuesRejectMalformedAndEmptyInput(t *testing.T) {
	for _, input := range []map[string]json.RawMessage{nil, {}, {"": json.RawMessage(`""`)}, {"value": json.RawMessage(`null`)}, {"value": json.RawMessage(`invalid`)}} {
		plain, err := credentialPlaintext(input)
		require.Error(t, err)
		require.Nil(t, plain)
	}
}
