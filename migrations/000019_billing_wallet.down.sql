BEGIN;
DROP INDEX uq_api_call_request;
ALTER TABLE api_call_log ALTER COLUMN cost_units TYPE integer;
ALTER TABLE api_call DROP COLUMN request_hash,DROP COLUMN idempotency_key,DROP COLUMN currency,
    DROP COLUMN charge_units,DROP COLUMN price_cents,DROP COLUMN billing_mode,DROP COLUMN app_id;
ALTER TABLE api_route DROP COLUMN currency,DROP COLUMN units_per_call,DROP COLUMN price_cents;
DROP TABLE recharge_code;
DROP TABLE call_charge;
DROP TABLE wallet_txn;
DROP FUNCTION reject_wallet_txn_mutation();
DROP TABLE wallet;
COMMIT;
