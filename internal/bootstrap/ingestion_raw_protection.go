package bootstrap

import (
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

func (s *rawStore) rawWire(identity ingestion.RawIdentity, shop int64, resource string, records []ingestion.RawRecord) ([]rawWire, []ingestion.RawRecord, error) {
	indexed := append([]ingestion.RawRecord(nil), records...)
	if resource != "order" {
		return uniqueRaw(indexed), indexed, nil
	}
	if s.protector == nil {
		return nil, nil, errors.New("order raw encryption is required")
	}
	originals := make(map[rawKey]string, len(records))
	for i := range indexed {
		external, hash, err := s.protector.Index(identity, shop, records[i].ExternalID, records[i].PayloadSHA256)
		if err != nil {
			return nil, nil, err
		}
		indexed[i].ExternalID, indexed[i].PayloadSHA256 = external, hash
		originals[rawKey{external, hash}] = records[i].ExternalID
	}
	wire := uniqueRaw(indexed)
	for i := range wire {
		wire[i].OriginalExternalID = originals[rawKey{wire[i].ExternalID, wire[i].Hash}]
	}
	return wire, indexed, nil
}

func rawDirectoryInput(rows []rawWire) ([]byte, error) {
	directory := make([]rawWire, len(rows))
	for i, row := range rows {
		directory[i] = rawWire{ExternalID: row.ExternalID, Hash: row.Hash, FetchedAt: row.FetchedAt}
	}
	return json.Marshal(directory)
}

func (s *rawStore) protectOrderRaw(identity ingestion.RawIdentity, shop int64, row rawWire) (rawWire, error) {
	ref := ingestion.ProtectedRawRef{RawIdentity: identity, ShopID: shop, ID: row.ID, ExternalID: row.ExternalID, PayloadHash: row.Hash, FetchedAt: row.FetchedAt}
	item := ingestion.RawItem{ExternalID: row.OriginalExternalID, Payload: row.Payload, FetchedAt: row.FetchedAt, APIName: row.APIName, RequestMeta: row.RequestMeta, HTTPStatus: row.HTTPStatus}
	payload, err := s.protector.Seal(ref, item)
	if err != nil {
		return rawWire{}, err
	}
	row.Payload, row.APIName, row.RequestMeta = payload, "protected_order", json.RawMessage(`{"protected":true}`)
	return row, nil
}
