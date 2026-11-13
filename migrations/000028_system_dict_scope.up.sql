BEGIN;
ALTER TABLE sys_dict ADD COLUMN scope text NOT NULL DEFAULT 'business'
    CONSTRAINT ck_sys_dict_scope CHECK (scope IN ('system','business'));
UPDATE sys_dict SET scope='system' WHERE type IN ('user_status','sync_run_status');
COMMIT;
