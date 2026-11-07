BEGIN;

-- ADR-019: business aggregates deliberately have no physical foreign keys.
CREATE TABLE sales_order (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    platform_id bigint NOT NULL,
    external_order_id text NOT NULL CHECK (btrim(external_order_id) <> ''),
    order_no text NOT NULL DEFAULT '',
    lifecycle_status text NOT NULL CHECK (lifecycle_status IN ('unknown','pending_payment','paid','shipped','completed','cancelled')),
    fulfillment_status text NOT NULL CHECK (fulfillment_status IN ('unknown','unshipped','shipped','delivered')),
    payment_status text NOT NULL CHECK (payment_status IN ('unknown','unpaid','paid','partial_refund','refunded')),
    after_sales_status text NOT NULL CHECK (after_sales_status IN ('unknown','none','refunding','partial_refunded','refunded')),
    cancel_status text NOT NULL CHECK (cancel_status IN ('unknown','none','requested','rejected','cancelled')),
    platform_status text NOT NULL DEFAULT '',
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    goods_amount_cents bigint NOT NULL CHECK (goods_amount_cents >= 0),
    shipping_fee_cents bigint NOT NULL CHECK (shipping_fee_cents >= 0),
    discount_cents bigint NOT NULL CHECK (discount_cents >= 0),
    total_amount_cents bigint NOT NULL CHECK (total_amount_cents >= 0),
    paid_amount_cents bigint NOT NULL CHECK (paid_amount_cents >= 0),
    refunded_amount_cents bigint NOT NULL CHECK (refunded_amount_cents >= 0 AND refunded_amount_cents <= paid_amount_cents),
    formula_delta_cents bigint NOT NULL,
    payment_delta_cents bigint NOT NULL,
    payment_settled boolean NOT NULL,
    item_count integer NOT NULL DEFAULT 0 CHECK (item_count >= 0),
    last_refunded_at timestamptz,
    refund_rejection_code text NOT NULL DEFAULT '',
    revision bigint CHECK (revision >= 0),
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    raw_id bigint NOT NULL,
    source_fetched_at timestamptz NOT NULL,
    mapping_checksum text NOT NULL,
    content_hash text NOT NULL,
    placed_at timestamptz,
    synced_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT uq_order_external UNIQUE (tenant_id, shop_id, external_order_id)
);
CREATE INDEX idx_order_list ON sales_order (tenant_id, shop_id, lifecycle_status, placed_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_order_time ON sales_order (tenant_id, placed_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE order_item (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    order_id bigint NOT NULL,
    external_item_id text NOT NULL CHECK (btrim(external_item_id) <> ''),
    listing_id bigint,
    listing_sku_id bigint,
    sku_id bigint,
    title text NOT NULL DEFAULT '',
    sku_title text NOT NULL DEFAULT '',
    image_url text NOT NULL DEFAULT '',
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    price_cents bigint NOT NULL CHECK (price_cents >= 0),
    qty bigint NOT NULL CHECK (qty > 0),
    discount_cents bigint NOT NULL CHECK (discount_cents >= 0),
    amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
    order_discount_cents bigint NOT NULL CHECK (order_discount_cents >= 0 AND order_discount_cents <= amount_cents),
    allocation_source text NOT NULL CHECK (allocation_source IN ('platform','largest_remainder_v1','unknown')),
    refunded_amount_cents bigint CHECK (refunded_amount_cents >= 0),
    refund_status text NOT NULL CHECK (refund_status IN ('unknown','none','refunding','partial_refunded','refunded')),
    last_refunded_at timestamptz,
    refund_rejection_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT uq_order_item UNIQUE (tenant_id, order_id, external_item_id)
);
CREATE INDEX idx_order_item_order ON order_item (tenant_id, order_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_order_item_sku ON order_item (tenant_id, sku_id);

CREATE TABLE order_address (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    order_id bigint NOT NULL,
    receiver_name_cipher bytea NOT NULL CHECK (octet_length(receiver_name_cipher) >= 16),
    receiver_name_nonce bytea NOT NULL CHECK (octet_length(receiver_name_nonce) = 12),
    phone_cipher bytea NOT NULL CHECK (octet_length(phone_cipher) >= 16),
    phone_nonce bytea NOT NULL CHECK (octet_length(phone_nonce) = 12),
    address_cipher bytea NOT NULL CHECK (octet_length(address_cipher) >= 16),
    address_nonce bytea NOT NULL CHECK (octet_length(address_nonce) = 12),
    key_version integer NOT NULL CHECK (key_version > 0),
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_order_address UNIQUE (tenant_id, order_id),
    CHECK (receiver_name_nonce <> phone_nonce AND phone_nonce <> address_nonce AND receiver_name_nonce <> address_nonce)
);

CREATE TABLE order_status_history (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    order_id bigint NOT NULL,
    dimension text NOT NULL CHECK (dimension IN ('lifecycle','fulfillment','payment','after_sales','cancel','aggregate')),
    from_status text NOT NULL,
    to_status text NOT NULL,
    event_kind text NOT NULL CHECK (event_kind IN ('snapshot','command','correction','conflict')),
    reason text NOT NULL,
    evidence jsonb NOT NULL,
    event_key text NOT NULL,
    occurred_at timestamptz,
    observed_at timestamptz NOT NULL,
    CONSTRAINT uq_order_history_event UNIQUE (tenant_id, order_id, event_key)
);
CREATE INDEX idx_order_history ON order_status_history (tenant_id, order_id, observed_at, id);
CREATE INDEX idx_order_conflict ON order_status_history (tenant_id, order_id, observed_at) WHERE event_kind = 'conflict';

CREATE FUNCTION order_history_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'order history is append only' USING ERRCODE = '55000';
END;
$$;
CREATE TRIGGER order_history_append_only BEFORE UPDATE OR DELETE ON order_status_history
    FOR EACH ROW EXECUTE FUNCTION order_history_append_only();

-- M5b owns processing; this milestone installs only its durable storage.
CREATE TABLE order_sync_pending (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    platform_id bigint NOT NULL,
    external_order_id text NOT NULL,
    revision bigint,
    listed_updated_at timestamptz,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','done','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_retry_at timestamptz,
    reason_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_order_sync_pending UNIQUE (tenant_id, shop_id, external_order_id)
);
CREATE INDEX idx_order_sync_pending ON order_sync_pending (tenant_id, shop_id, status, next_retry_at);
COMMIT;
