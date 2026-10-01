BEGIN;
SET LOCAL lock_timeout='5s';
CREATE TABLE vault.folders (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id uuid NOT NULL REFERENCES vault.users(id),
 parent_id uuid,
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100 AND name=btrim(name)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(owner_id,id),
 FOREIGN KEY(owner_id,parent_id) REFERENCES vault.folders(owner_id,id),
 UNIQUE NULLS NOT DISTINCT(owner_id,parent_id,name)
);
CREATE INDEX folders_owner_parent_idx ON vault.folders(owner_id,parent_id,id);
ALTER TABLE vault.files ADD COLUMN folder_id uuid;
ALTER TABLE vault.files ADD CONSTRAINT files_owned_folder_fk
 FOREIGN KEY(owner_id,folder_id) REFERENCES vault.folders(owner_id,id);
CREATE INDEX files_owner_folder_idx ON vault.files(owner_id,folder_id,created_at DESC,id DESC);
GRANT SELECT,INSERT,DELETE ON vault.folders TO vault_runtime;
GRANT UPDATE(name) ON vault.folders TO vault_runtime;
GRANT UPDATE(folder_id) ON vault.files TO vault_runtime;
COMMIT;
