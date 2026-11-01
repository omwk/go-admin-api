package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/omwk/go-admin-api/internal/modules/shop/credential"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

func TestCredentialIntegrationT24SixChannelsAndVersionChain(t *testing.T) {
	// REQ-SHP-003/004/006/007/010: every non-secret channel excludes random private values.
	f := newCredentialFixture(t)
	created, values := f.create(t, "cookie")
	f.assertEncryption(t, created, values)
	oldRow, oldVersion := f.readStored(t, created.ID, 1)
	f.assertMetadataAndCheck(t, created)
	newValues := map[string]string{"private_value": f.randomValue(t), "user_agent": f.randomValue(t)}
	response, envelope := f.request(t, http.MethodPut, credentialPath(created.ID), map[string]any{"values": newValues})
	require.Equal(t, 200, response.Code, response.Body.String())
	var rotated shop.CredentialView
	require.NoError(t, json.Unmarshal(envelope.Data, &rotated))
	require.Equal(t, 2, rotated.VersionNo)
	f.assertEncryption(t, rotated, newValues)
	current, retired := f.readStored(t, created.ID, 1)
	_, latest := f.readStored(t, created.ID, 2)
	require.Equal(t, latest.ID, *current.CurrentVersionID)
	require.NotEqual(t, *oldRow.CurrentVersionID, *current.CurrentVersionID)
	require.NotNil(t, retired.RetiredAt)
	require.NotNil(t, retired.PurgeAfter)
	require.Equal(t, 90*24*time.Hour, retired.PurgeAfter.Sub(*retired.RetiredAt))
	retired.RetiredAt, retired.PurgeAfter = nil, nil
	require.Equal(t, oldVersion, retired, "retirement must not rewrite immutable version fields")
	f.assertDisableAndAudits(t, created.ID)
	f.databaseSnapshot(t)
	f.noLeaks(t, "application logs", f.logs.text())
	f.noLeaks(t, "SQL logs", f.sqlLogs.text())
	require.Contains(t, f.logs.text(), "HTTP request")
	require.Contains(t, f.sqlLogs.text(), "INSERT INTO")
}

func (f *credentialFixture) assertEncryption(t *testing.T, view shop.CredentialView, expected map[string]string) {
	t.Helper()
	row, version := f.readStored(t, view.ID, view.VersionNo)
	opened, err := credential.OpenVersion(f.sealer, 1, f.shop, row, version)
	require.NoError(t, err)
	defer clear(opened)
	var actual map[string]string
	require.NoError(t, json.Unmarshal(opened, &actual))
	require.True(t, len(expected) == len(actual), "decrypted field count differs")
	for key, value := range expected {
		require.True(t, value == actual[key], "decrypted value differs")
	}
	require.Equal(t, crypto.Fingerprint(opened), view.Fingerprint)
	require.Len(t, version.Nonce, 12)
	require.NotEmpty(t, version.CipherText)
}

func (f *credentialFixture) assertMetadataAndCheck(t *testing.T, view shop.CredentialView) {
	t.Helper()
	response, envelope := f.request(t, "GET", f.shopPath()+"/credentials", nil)
	require.Equal(t, 200, response.Code)
	var page shop.CredentialPage
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List, 1)
	detail, err := f.service.GetCredential(f.ctx, f.actor, view.ID)
	require.NoError(t, err)
	require.Equal(t, view, detail)
	before, _ := f.readStored(t, view.ID, 1)
	response, envelope = f.request(t, "POST", credentialPath(view.ID)+"/check", nil)
	require.Equal(t, 400, response.Code)
	require.Equal(t, shop.ErrUnsupported.Code, envelope.Code)
	after, _ := f.readStored(t, view.ID, 1)
	require.Equal(t, before, after, "unsupported check must not mutate health or timestamps")
	err = f.service.CheckCredential(f.ctx, f.actor, view.ID)
	require.ErrorIs(t, err, shop.ErrUnsupported)
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		f.noLeaks(t, "error chain", cause.Error())
	}
}

func (f *credentialFixture) assertDisableAndAudits(t *testing.T, id int64) {
	t.Helper()
	response, envelope := f.request(t, "POST", credentialPath(id)+"/disable", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var disabled shop.CredentialView
	require.NoError(t, json.Unmarshal(envelope.Data, &disabled))
	require.Equal(t, "disabled", disabled.Status)
	response, envelope = f.request(t, "GET", credentialPath(id)+"/audits", nil)
	require.Equal(t, 200, response.Code)
	var audits shop.CredentialAuditPage
	require.NoError(t, json.Unmarshal(envelope.Data, &audits))
	counts := map[string]int{}
	for _, audit := range audits.List {
		counts[audit.Action]++
		if audit.Action == "read" {
			require.True(t, audit.Detail.MetadataOnly)
		}
		if audit.Action == "check" {
			require.Equal(t, "failure", audit.Result)
			require.Equal(t, "30401", audit.ErrorCode)
		}
	}
	require.Equal(t, map[string]int{"create": 1, "read": 2, "check": 2, "rotate": 1, "disable": 1}, counts)
}

func TestCredentialIntegrationT24SameKindSwapBothDirections(t *testing.T) {
	// REQ-SHP-004: trusted relationships rebuild AAD, even if the stored aad is also swapped.
	f := newCredentialFixture(t)
	first, firstValues := f.create(t, "oauth2")
	second, secondValues := f.create(t, "oauth2")
	f.assertEncryption(t, first, firstValues)
	f.assertEncryption(t, second, secondValues)
	rows := make([]shopmodel.Credential, 2)
	versions := make([]shopmodel.CredentialVersion, 2)
	rows[0], versions[0] = f.readStored(t, first.ID, 1)
	rows[1], versions[1] = f.readStored(t, second.ID, 1)
	for _, swapAAD := range []bool{false, true} {
		for i := range rows {
			other := versions[1-i]
			updates := map[string]any{"cipher_text": other.CipherText, "nonce": other.Nonce, "key_version": other.KeyVersion}
			if swapAAD {
				updates["aad"] = other.AAD
			}
			q, cancel := f.database.WithContext(f.ctx)
			require.NoError(t, q.Model(&shopmodel.CredentialVersion{}).Where("id = ?", versions[i].ID).Updates(updates).Error)
			cancel()
			_, swapped := f.readStored(t, rows[i].ID, 1)
			opened, err := credential.OpenVersion(f.sealer, 1, f.shop, rows[i], swapped)
			require.Error(t, err)
			require.Nil(t, opened)
			f.noLeaks(t, "decryption error", err.Error())
		}
	}
	require.False(t, bytes.Equal(versions[0].CipherText, versions[1].CipherText))
}

func credentialTestPage() repo.Page { return repo.Page{Number: 1, Size: 20, Sort: "-created_at"} }
