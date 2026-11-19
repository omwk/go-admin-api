BEGIN;
ALTER TABLE auth_token DROP CONSTRAINT auth_token_purpose_check;
ALTER TABLE auth_token
 ADD CONSTRAINT auth_token_purpose_check CHECK(purpose IN ('invite','reset','verify','email_change')),
 ADD COLUMN original_email text,
 ADD COLUMN new_email text,
 ADD CONSTRAINT auth_token_email_change_check CHECK(purpose<>'email_change' OR (new_email IS NOT NULL AND new_email<>''));
COMMIT;
