package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

// NewRawService binds raw persistence to a trusted shop resolver. No routes or
// worker claims are registered until the orchestration batch supplies access.
func NewRawService(database *db.DB, resolve ingestion.RawResolver, sealers ...crypto.Sealer) (*ingestion.Service, error) {
	if database == nil {
		return nil, errors.New("raw database is required")
	}
	store := &rawStore{database: database}
	if len(sealers) > 0 && sealers[0] != nil {
		var err error
		store.protector, err = ingestion.NewRawProtector(sealers[0])
		if err != nil {
			return nil, err
		}
	}
	return ingestion.NewService(store, resolve)
}

type rawStore struct {
	database  *db.DB
	protector *ingestion.RawProtector
}

func (s *rawStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

type rawKey struct{ ExternalID, Hash string }
type rawDirectoryRow struct {
	RawID         int64
	ExternalID    string
	PayloadSHA256 string
	FetchedAt     time.Time
}
type rawWire struct {
	OriginalExternalID string          `json:"-"`
	ID                 int64           `json:"id"`
	ExternalID         string          `json:"external_id"`
	Hash               string          `json:"payload_sha256"`
	FetchedAt          time.Time       `json:"fetched_at"`
	Payload            json.RawMessage `json:"payload"`
	Size               int             `json:"payload_size"`
	APIName            string          `json:"api_name"`
	RequestMeta        json.RawMessage `json:"request_meta"`
	HTTPStatus         *int            `json:"http_status"`
}

func (s *rawStore) SaveBatch(ctx context.Context, identity ingestion.RawIdentity, shopID int64, resource string, records []ingestion.RawRecord) ([]ingestion.RawRef, error) {
	if len(records) == 0 || len(records) > 500 {
		return nil, errors.New("raw batch must contain 1 to 500 records")
	}
	wire, indexed, err := s.rawWire(identity, shopID, resource, records)
	if err != nil {
		return nil, err
	}
	input, err := rawDirectoryInput(wire)
	if err != nil {
		return nil, errors.New("encode raw batch")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var claimed []rawDirectoryRow
	err = q.Raw(`INSERT INTO raw_dedup
        (tenant_id, platform_id, shop_id, resource_type, external_id, payload_sha256, raw_id, fetched_at)
        SELECT ?, ?, ?, ?, x.external_id, x.payload_sha256,
            nextval(pg_get_serial_sequence('raw_payload', 'id')), x.fetched_at
        FROM jsonb_to_recordset(?::jsonb) AS x(external_id text, payload_sha256 text, fetched_at timestamptz)
        ORDER BY x.external_id, x.payload_sha256
        ON CONFLICT (tenant_id, platform_id, shop_id, resource_type, external_id, payload_sha256) DO NOTHING
        RETURNING raw_id, external_id, payload_sha256, fetched_at`,
		identity.TenantID, identity.PlatformID, shopID, resource, string(input)).Scan(&claimed).Error
	if err != nil {
		return nil, fmt.Errorf("claim raw directory: %w", err)
	}
	var directory []rawDirectoryRow
	err = q.Raw(`SELECT d.raw_id, d.external_id, d.payload_sha256, d.fetched_at
        FROM raw_dedup d JOIN jsonb_to_recordset(?::jsonb) AS x(external_id text, payload_sha256 text)
          ON d.external_id = x.external_id AND d.payload_sha256 = x.payload_sha256
        WHERE d.tenant_id = ? AND d.platform_id = ? AND d.shop_id = ? AND d.resource_type = ?`,
		string(input), identity.TenantID, identity.PlatformID, shopID, resource).Scan(&directory).Error
	if err != nil {
		return nil, fmt.Errorf("read raw directory: %w", err)
	}
	if len(directory) != len(wire) {
		return nil, errors.New("raw directory claim not visible; retry batch")
	}
	fresh := make(map[rawKey]bool, len(claimed))
	for _, row := range claimed {
		fresh[rawKey{row.ExternalID, row.PayloadSHA256}] = true
	}
	refs := directoryRefs(directory, fresh)
	inserts := make([]rawWire, 0, len(claimed))
	for _, row := range wire {
		key := rawKey{row.ExternalID, row.Hash}
		if fresh[key] {
			row.ID = refs[key].ID
			if resource == "order" {
				row, err = s.protectOrderRaw(identity, shopID, row)
				if err != nil {
					return nil, err
				}
			}
			inserts = append(inserts, row)
		}
	}
	if err := s.insertRaw(ctx, identity, shopID, resource, inserts); err != nil {
		return nil, err
	}
	ordered := orderedRawRefs(indexed, refs)
	for i := range ordered {
		ordered[i].ExternalID = records[i].ExternalID
	}
	return ordered, nil
}

func uniqueRaw(records []ingestion.RawRecord) []rawWire {
	seen := make(map[rawKey]bool, len(records))
	wire := make([]rawWire, 0, len(records))
	for _, row := range records {
		key := rawKey{row.ExternalID, row.PayloadSHA256}
		if seen[key] {
			continue
		}
		seen[key] = true
		wire = append(wire, rawWire{ExternalID: row.ExternalID, Hash: row.PayloadSHA256, FetchedAt: row.FetchedAt,
			Payload: row.Payload, Size: row.PayloadSize, APIName: row.APIName, RequestMeta: row.RequestMeta, HTTPStatus: row.HTTPStatus})
	}
	return wire
}

func directoryRefs(rows []rawDirectoryRow, fresh map[rawKey]bool) map[rawKey]ingestion.RawRef {
	refs := make(map[rawKey]ingestion.RawRef, len(rows))
	for _, row := range rows {
		key := rawKey{row.ExternalID, row.PayloadSHA256}
		refs[key] = ingestion.RawRef{ID: row.RawID, ExternalID: row.ExternalID, FetchedAt: row.FetchedAt.UTC(), IsNew: fresh[key]}
	}
	return refs
}

func orderedRawRefs(records []ingestion.RawRecord, refs map[rawKey]ingestion.RawRef) []ingestion.RawRef {
	result := make([]ingestion.RawRef, 0, len(records))
	for _, row := range records {
		key := rawKey{row.ExternalID, row.PayloadSHA256}
		result = append(result, refs[key])
		ref := refs[key]
		ref.IsNew = false
		refs[key] = ref
	}
	return result
}

func (s *rawStore) insertRaw(ctx context.Context, identity ingestion.RawIdentity, shopID int64, resource string, rows []rawWire) error {
	if len(rows) == 0 {
		return nil
	}
	input, err := json.Marshal(rows)
	if err != nil {
		return errors.New("encode new raw records")
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	err = q.Exec(`INSERT INTO raw_payload
        (id, tenant_id, platform_id, shop_id, resource_type, external_id, payload_sha256, fetched_at,
         payload, payload_size, api_name, request_meta, http_status) OVERRIDING SYSTEM VALUE
        SELECT x.id, ?, ?, ?, ?, x.external_id, x.payload_sha256, x.fetched_at,
            x.payload, x.payload_size, x.api_name, x.request_meta, x.http_status
        FROM jsonb_to_recordset(?::jsonb) AS x(id bigint, external_id text, payload_sha256 text, fetched_at timestamptz,
            payload jsonb, payload_size integer, api_name text, request_meta jsonb, http_status integer)`,
		identity.TenantID, identity.PlatformID, shopID, resource, string(input)).Error
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23514" && pg.TableName == "raw_payload" && pg.ConstraintName == "" {
			return ingestion.ErrPartitionMissing
		}
		return fmt.Errorf("insert raw records: %w", err)
	}
	return nil
}
