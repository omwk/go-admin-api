package bootstrap

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/stretchr/testify/require"
)

func TestCredentialIntegrationT24AuditFailureRollsBackEveryEntry(t *testing.T) {
	// REQ-SHP-007: failing audit persistence must not commit business changes or metadata reads.
	f := newCredentialFixture(t)
	created, _ := f.create(t, "api_key")
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION deny_credential_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'audit persistence unavailable'; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER deny_credential_audit BEFORE INSERT ON credential_audit
FOR EACH ROW EXECUTE FUNCTION deny_credential_audit()`))
	before := f.databaseSnapshot(t)
	values := map[string]string{"private_value": f.randomValue(t)}
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"POST", f.shopPath() + "/credentials", map[string]any{"kind": "oauth2", "values": values}},
		{"GET", f.shopPath() + "/credentials", nil},
		{"PUT", credentialPath(created.ID), map[string]any{"values": values}},
		{"POST", credentialPath(created.ID) + "/disable", nil},
		{"POST", credentialPath(created.ID) + "/check", nil},
		{"GET", credentialPath(created.ID) + "/audits", nil},
	} {
		response, envelope := f.request(t, request.method, request.path, request.body)
		require.Equal(t, 500, response.Code, request.path)
		require.Equal(t, errs.ErrInternal, envelope.Code)
		require.Equal(t, before, f.databaseSnapshot(t))
	}
	view, err := f.service.GetCredential(f.ctx, f.actor, created.ID)
	require.Error(t, err)
	require.Zero(t, view.ID)
	require.Equal(t, before, f.databaseSnapshot(t))
	f.noLeaks(t, "failed SQL logs", f.sqlLogs.text())
	f.noLeaks(t, "failed application logs", f.logs.text())
}

func TestCredentialIntegrationT24EncryptionFailureHasSafeErrorChain(t *testing.T) {
	f := newCredentialFixture(t)
	marker := f.randomValue(t)
	bad := &credentialFailingSealer{err: errors.New(marker)}
	service, err := shop.NewService(f.store, f.adminFixture.store.Authorize, bad)
	require.NoError(t, err)
	before := f.databaseSnapshot(t)
	value, err := json.Marshal(marker)
	require.NoError(t, err)
	view, err := service.CreateCredential(f.ctx, f.actor, f.shop.ID, shop.CredentialCreateRequest{Kind: "cookie", Values: map[string]json.RawMessage{"private_value": value}})
	require.Error(t, err)
	require.Zero(t, view.ID)
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		f.noLeaks(t, "encryption error chain", cause.Error())
	}
	require.Equal(t, before, f.databaseSnapshot(t))
	// Database drivers may attach raw input in their private Detail; the adapter removes it.
	f.noLeaks(t, "database error chain", credentialDBError(errors.New(marker)).Error())
}

type credentialFailingSealer struct{ err error }

func (s *credentialFailingSealer) Seal([]byte, string) ([]byte, []byte, int, error) {
	return nil, nil, 0, s.err
}
func (s *credentialFailingSealer) Open([]byte, []byte, string, int) ([]byte, error) {
	return nil, s.err
}

func TestCredentialIntegrationT24RejectsSecretsOutsideValues(t *testing.T) {
	f := newCredentialFixture(t)
	created, _ := f.create(t, "cookie")
	marker := f.randomValue(t)
	before := f.databaseSnapshot(t)
	for _, extra := range []any{map[string]any{"user_agent": marker}, map[string]any{"scope": map[string]string{"private_value": marker}}, []string{marker}, nil} {
		response, _ := f.request(t, "PUT", credentialPath(created.ID), map[string]any{"values": map[string]string{"private_value": marker}, "extra": extra})
		require.Equal(t, 400, response.Code)
	}
	require.Equal(t, before, f.databaseSnapshot(t))
}
