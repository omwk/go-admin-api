package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestShareIntegrationOneTimeSecretPasswordAndSafeReplay(t *testing.T) {
	// REQ-SHR-003/005: business receipt survives response loss, the secret does not.
	f := newShareAccessFixture(t)
	c := f.customer(t, "private")
	in := f.publication()
	in.CustomerID = c.ID
	r, _ := f.request(t, "POST", "/share-links", in)
	require.Equal(t, 400, r.Code)
	in.Password = f.randomValue(t)
	r, envelope := f.keyed(t, "POST", "/share-links", "issue-once", in)
	require.Equal(t, 201, r.Code)
	var first share.LinkReceipt
	require.NoError(t, json.Unmarshal(envelope.Data, &first))
	require.NotEmpty(t, first.URL)
	r, envelope = f.keyed(t, "POST", "/share-links", "issue-once", in)
	require.Equal(t, 201, r.Code)
	var replay share.LinkReceipt
	require.NoError(t, json.Unmarshal(envelope.Data, &replay))
	require.Equal(t, first.ID, replay.ID)
	require.True(t, replay.SecretDelivered)
	require.Empty(t, replay.URL)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row share.Link
	require.NoError(t, q.Select("id,token_hash,token_prefix,password_hash").Where("id=?", first.ID).Take(&row).Error)
	token := shareToken(first)
	sum := sha256.Sum256([]byte(token))
	require.Equal(t, hex.EncodeToString(sum[:]), row.TokenHash)
	require.Equal(t, token[:8], row.TokenPrefix)
	cost, err := bcrypt.Cost([]byte(*row.PasswordHash))
	require.NoError(t, err)
	require.Equal(t, 12, cost)
	var records []string
	require.NoError(t, q.Raw(`SELECT row_to_json(l)::text FROM share_link l UNION ALL SELECT row_to_json(s)::text FROM share_snapshot s UNION ALL SELECT row_to_json(i)::text FROM idempotency_key i UNION ALL SELECT row_to_json(a)::text FROM audit_log a`).Scan(&records).Error)
	for _, record := range records {
		require.NotContains(t, record, token)
		require.NotContains(t, record, in.Password)
	}
	in.Title += " changed"
	r, e := f.keyed(t, "POST", "/share-links", "issue-once", in)
	require.Equal(t, 409, r.Code)
	require.Equal(t, 10006, e.Code)
	require.NotContains(t, f.logs.text(), token)
	require.NotContains(t, f.sqlLogs.text(), token)
}

func TestShareIntegrationRegenerateCreatesNewLinkAndExplicitRevocation(t *testing.T) {
	f := newShareAccessFixture(t)
	old := f.issue(t, f.publication())
	path := fmt.Sprintf("/share-links/%d/regenerate", old.ID)
	in := f.publication()
	r, _ := f.request(t, "POST", path, in)
	require.Equal(t, 400, r.Code)
	for _, revoke := range []bool{false, true} {
		in.RevokeOld = &revoke
		r, e := f.request(t, "POST", path, in)
		require.Equal(t, 201, r.Code)
		var next share.LinkReceipt
		require.NoError(t, json.Unmarshal(e.Data, &next))
		require.NotEqual(t, old.ID, next.ID)
		require.NotEqual(t, old.URL, next.URL)
		oldResponse := f.anonymous(t, "GET", old.URL, "192.0.2.1", nil, nil)
		if revoke {
			require.Equal(t, 404, oldResponse.Code)
		} else {
			require.Equal(t, 200, oldResponse.Code)
		}
		require.Equal(t, 200, f.anonymous(t, "GET", next.URL, "192.0.2.1", nil, nil).Code)
	}
	r, _ = f.request(t, "GET", "/share-links", nil)
	require.Equal(t, 200, r.Code)
	for _, field := range []string{"token_hash", "password_hash", "url", "customer_id", "price_list_id"} {
		require.NotContains(t, r.Body.String(), `"`+field+`"`)
	}
}
