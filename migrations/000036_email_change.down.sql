BEGIN;
DELETE FROM auth_token WHERE purpose='email_change';
ALTER TABLE auth_token
 DROP CONSTRAINT auth_token_email_change_check,
 DROP COLUMN new_email,
 DROP COLUMN original_email,
 DROP CONSTRAINT auth_token_purpose_check;
ALTER TABLE auth_token ADD CONSTRAINT auth_token_purpose_check CHECK(purpose IN ('invite','reset','verify'));
COMMIT;
