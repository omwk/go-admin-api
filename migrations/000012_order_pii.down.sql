ALTER TABLE order_address DROP CONSTRAINT order_address_phone_mask, DROP CONSTRAINT order_address_name_mask,
    DROP COLUMN phone_mask, DROP COLUMN receiver_name_mask;
ALTER TABLE sys_refresh_token DROP COLUMN reauth_at;
