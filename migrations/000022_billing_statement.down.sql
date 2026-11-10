BEGIN;
DROP FUNCTION billing_retention_candidates(timestamptz);
DROP TABLE billing_adjustment;
DROP FUNCTION protect_billing_adjustment();
DROP TABLE billing_statement_line;
DROP FUNCTION protect_billing_statement_line();
DROP TABLE billing_statement;
DROP FUNCTION protect_billing_statement();
COMMIT;
