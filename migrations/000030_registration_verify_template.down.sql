BEGIN;
DELETE FROM notify_template WHERE tenant_id IS NULL AND code='identity.verify';
COMMIT;
