package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestOrderRawIntegrationEnvelopeProtectsMetadataAndDeduplicates(t *testing.T) {
	f := newOrderQueryFixture(t)
	marker := "PII-CANARY-123"
	item := ingestion.RawItem{ExternalID: marker, Payload: json.RawMessage(`{ "address" : "PII-CANARY-123" }`), RequestMeta: json.RawMessage(`{"private":"PII-CANARY-123"}`), APIName: marker, FetchedAt: time.Now().UTC().Truncate(time.Microsecond)}
	service := rawServiceFor(t, f.database, 1, f.shop.PlatformID, f.sealer)
	refs, err := service.SaveRaw(f.ctx, f.shop.ID, "order", []ingestion.RawItem{item})
	require.NoError(t, err)
	duplicate := item
	duplicate.FetchedAt = duplicate.FetchedAt.Add(time.Hour)
	duplicate.Payload = json.RawMessage(`{"address":"PII-CANARY-123"}`)
	again, err := service.SaveRaw(f.ctx, f.shop.ID, "order", []ingestion.RawItem{duplicate})
	require.NoError(t, err)
	require.Equal(t, refs[0].ID, again[0].ID)
	require.Equal(t, refs[0].FetchedAt, again[0].FetchedAt)
	require.False(t, again[0].IsNew)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var row mappingRawSample
	require.NoError(t, q.Table("raw_payload").Select("tenant_id,platform_id,shop_id,id,external_id,payload_sha256 AS payload_hash,fetched_at,resource_type,payload").Where("id=?", refs[0].ID).Take(&row).Error)
	protector, err := ingestion.NewRawProtector(f.sealer)
	require.NoError(t, err)
	ref := ingestion.ProtectedRawRef{RawIdentity: ingestion.RawIdentity{TenantID: row.TenantID, PlatformID: row.PlatformID}, ShopID: row.ShopID, ID: row.ID, ExternalID: row.ExternalID, PayloadHash: row.PayloadHash, FetchedAt: row.FetchedAt}
	opened, err := protector.Open(ref, row.Payload)
	require.NoError(t, err)
	require.Equal(t, item.Payload, opened.Payload, "exact original whitespace survives encryption and dedup")
	require.Equal(t, item.RequestMeta, opened.RequestMeta)
	require.Equal(t, item.APIName, opened.APIName)
	require.Equal(t, item.ExternalID, opened.ExternalID)
	assertPIIAbsentFromDatabase(t, f.credentialFixture, marker)
}

func TestOrderRawIntegrationPublishPreflightKeepsResultsPIIFree(t *testing.T) {
	f, platform := newOrderSyncFixture(t, 1)
	piiSyncCanary(t, f, platform, "PII-CANARY-123")
	f.execute(t, f.run(t))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var version struct {
		ProfileID int64
		Spec      json.RawMessage
	}
	require.NoError(t, q.Table("mapping_version").Select("profile_id,spec").Where("id=?", f.mappingID).Take(&version).Error)
	// Exercise the real preflight, with a draft copied from the active order mapping.
	require.NoError(t, q.Table("mapping_version").Where("id=?", f.mappingID).Update("status", "draft").Error)
	request := ingestion.PublishRequest{VersionID: f.mappingID, Spec: version.Spec}
	_, _, err := PublishMapping(f.ctx, f.database, version.ProfileID, 0, request)
	require.Error(t, err, "protected raw cannot be interpreted without keys")
	require.NotContains(t, err.Error(), "PII-CANARY-123")
	_, result, err := PublishMapping(f.ctx, f.database, version.ProfileID, 0, request, f.sealer)
	require.NoError(t, err)
	require.Equal(t, 1, result.Checked)
	require.Empty(t, result.Errors)
	assertPIIAbsentFromDatabase(t, f.credentialFixture, "PII-CANARY-123")
}
