BEGIN;
DROP TABLE replay_run;
DROP TABLE dead_letter;
DROP TABLE sync_error;
DROP TABLE sync_run;
DROP TABLE sync_cursor;
DROP TABLE sync_job;
COMMIT;
