-- ADR-019: session-bound step-up authentication and bounded safe address projections.
ALTER TABLE sys_refresh_token ADD COLUMN reauth_at timestamptz;
ALTER TABLE order_address
    ADD COLUMN receiver_name_mask text NOT NULL DEFAULT '*',
    ADD COLUMN phone_mask text NOT NULL DEFAULT '***********',
    ADD CONSTRAINT order_address_name_mask CHECK (char_length(receiver_name_mask) BETWEEN 1 AND 2 AND right(receiver_name_mask,1)='*'),
    ADD CONSTRAINT order_address_phone_mask CHECK (phone_mask ~ '^[0-9*][*]{10}$');
