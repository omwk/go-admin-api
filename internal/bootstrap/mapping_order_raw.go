package bootstrap

import (
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

type mappingRawSample struct {
	TenantID, PlatformID, ShopID, ID      int64
	ExternalID, PayloadHash, ResourceType string
	FetchedAt                             time.Time
	Payload                               json.RawMessage
}

// Order plaintext stays inside publication preflight; only safe validation results leave it.
func (s *mappingPublishBackend) openMappingSamples(rows []mappingRawSample) ([]json.RawMessage, error) {
	samples := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		payload := row.Payload
		if row.ResourceType == "order" {
			ref := ingestion.ProtectedRawRef{RawIdentity: ingestion.RawIdentity{TenantID: row.TenantID, PlatformID: row.PlatformID}, ShopID: row.ShopID, ID: row.ID, ExternalID: row.ExternalID, PayloadHash: row.PayloadHash, FetchedAt: row.FetchedAt}
			opened, err := s.protector.Open(ref, payload)
			if err != nil {
				for _, sample := range samples {
					clear(sample)
				}
				return nil, err
			}
			clear(opened.RequestMeta)
			payload = opened.Payload
		}
		samples = append(samples, payload)
	}
	return samples, nil
}
