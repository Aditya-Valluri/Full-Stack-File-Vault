BEGIN;
SET LOCAL lock_timeout='5s';
CREATE TABLE vault.file_tags (
 file_id uuid NOT NULL REFERENCES vault.files(id) ON DELETE CASCADE,
 tag text NOT NULL CHECK (tag ~ '^[a-z0-9][a-z0-9 _-]{0,31}$' AND tag=btrim(tag)),
 PRIMARY KEY(file_id,tag)
);
CREATE INDEX file_tags_search_idx ON vault.file_tags(tag,file_id);
GRANT SELECT,INSERT,DELETE ON vault.file_tags TO vault_runtime;
COMMIT;
