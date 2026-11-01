BEGIN;

ALTER TABLE shop_credential DROP CONSTRAINT fk_shop_credential_current_version;
DROP TABLE credential_audit;
DROP TABLE credential_version;
DROP TABLE shop_credential;
DROP TABLE shop;
DROP TABLE platform;

COMMIT;
