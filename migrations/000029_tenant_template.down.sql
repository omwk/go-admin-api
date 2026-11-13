BEGIN;
ALTER TABLE tenant DROP COLUMN provisioned_from;
DROP TABLE tenant_template;
COMMIT;
