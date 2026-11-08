package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
)

func rollback(ctx context.Context, tx pgx.Tx) error {
	err := tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrTxClosed) {
		return nil
	}
	return err
}

func installFixtures(ctx context.Context) (result any, err error) {
	c, err := connect(ctx, true)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.Close(ctx)) }()
	tx, err := c.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rollback(ctx, tx)) }()
	shop, err := strconv.ParseInt(os.Getenv("SMOKE_SHOP_ID"), 10, 64)
	if err != nil || shop <= 0 {
		return nil, errors.New("shop fixture identity required")
	}
	jobs := map[string]int64{}
	for _, resource := range []string{"product", "order"} {
		raw, err := json.Marshal(mappingSpec(resource))
		if err != nil {
			return nil, err
		}
		compiled, err := ingestion.CompileFromVersion(raw)
		if err != nil {
			return nil, err
		}
		var profile, version, job int64
		err = tx.QueryRow(ctx, `INSERT INTO mapping_profile(tenant_id,platform_id,resource_type,name)
SELECT 1,platform_id,$2,'S3 smoke' FROM shop WHERE id=$1 RETURNING id`, shop, resource).Scan(&profile)
		if err != nil {
			return nil, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO mapping_version(tenant_id,profile_id,version_no,status,spec,checksum)
VALUES(1,$1,1,'published',$2,$3) RETURNING id`, profile, raw, compiled.Checksum).Scan(&version)
		if err != nil {
			return nil, err
		}
		options, err := json.Marshal(map[string]any{"mapping_version_id": version, "authorization_generation": 1, "max_pages_per_run": 4, "page_size": 100})
		if err != nil {
			return nil, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO sync_job(tenant_id,shop_id,resource_type,cron,enabled,options)
VALUES(1,$1,$2,'0 0 1 1 *',true,$3) RETURNING id`, shop, resource, options).Scan(&job)
		if err != nil {
			return nil, err
		}
		jobs[resource] = job
	}
	err = tx.Commit(ctx)
	return jobs, err
}

func mappingSpec(resource string) ingestion.Spec {
	s := ingestion.Spec{SchemaVersion: 1, Resource: resource, KeyField: "id", StatusTables: map[string]map[string]string{}}
	if resource == "product" {
		for _, pair := range [][2]string{{"external_product_id", "id"}, {"title", "title"}, {"currency", "currency"}, {"status", "status"}, {"stock_total", "stock"}, {"price_cents", "price"}} {
			f := ingestion.FieldSpec{Target: pair[0], Source: "$." + pair[1], Required: true}
			if f.Target == "price_cents" {
				f.Transform = "money_cents(value)"
			}
			s.Fields = append(s.Fields, f)
		}
		s.Collections = []ingestion.CollectionSpec{{Target: "skus", Source: "$.skus", KeyField: "external_sku_id", Fields: []ingestion.FieldSpec{
			{Target: "external_sku_id", Source: "$.id", Required: true}, {Target: "title", Source: "$.title"},
			{Target: "price_cents", Source: "$.price", Transform: "money_cents(value)"}, {Target: "stock", Source: "$.stock"}}}}
		return s
	}
	s.KeyField = "external_order_id"
	for _, key := range strings.Fields("external_order_id order_no lifecycle_status fulfillment_status payment_status after_sales_status cancel_status currency goods_amount_cents shipping_fee_cents discount_cents total_amount_cents paid_amount_cents refunded_amount_cents payment_settled revision placed_at items_complete items_has_more items_expected_count allocation_source") {
		s.Fields = append(s.Fields, ingestion.FieldSpec{Target: key, Source: "$." + key})
	}
	collection := ingestion.CollectionSpec{Target: "items", Source: "$.items", KeyField: "external_item_id"}
	for _, key := range strings.Fields("external_item_id title currency price_cents qty discount_cents amount_cents discount_eligible refund_status") {
		collection.Fields = append(collection.Fields, ingestion.FieldSpec{Target: key, Source: "$." + key})
	}
	s.Collections = []ingestion.CollectionSpec{collection}
	return s
}

func checkCredential(ctx context.Context) (result any, err error) {
	c, err := connect(ctx, true)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, c.Close(ctx)) }()
	var cipher, nonce []byte
	var aad string
	var version int
	err = c.QueryRow(ctx, `SELECT v.cipher_text,v.nonce,v.aad,v.key_version FROM credential_version v
JOIN shop_credential c ON c.current_version_id=v.id WHERE c.shop_id=$1`, os.Getenv("SMOKE_SHOP_ID")).Scan(&cipher, &nonce, &aad, &version)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(os.Getenv("CH_CRYPTO_MASTER_KEY"), "base64:"))
	if err != nil {
		return nil, err
	}
	defer clear(key)
	ring, err := crypto.NewKeyring(map[int][]byte{1: key}, 1)
	if err != nil {
		return nil, err
	}
	plain, err := ring.Open(cipher, nonce, aad, version)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var values map[string]string
	if err := json.Unmarshal(plain, &values); err != nil {
		return nil, err
	}
	return map[string]any{"encrypted": len(cipher) > 0, "decrypted_matches": values["private_value"] == os.Getenv("SMOKE_CREDENTIAL")}, nil
}
