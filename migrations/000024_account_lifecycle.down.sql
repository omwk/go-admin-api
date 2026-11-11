BEGIN;
DELETE FROM notify_template WHERE tenant_id IS NULL AND code IN ('identity.invite','identity.reset');
DROP TABLE auth_token;
DROP TABLE password_history;
DROP INDEX uq_sys_user_email;
DROP INDEX uq_sys_user_phone;
ALTER TABLE sys_user DROP COLUMN totp_secret, DROP COLUMN force_password_change, DROP COLUMN password_changed_at;
COMMIT;
