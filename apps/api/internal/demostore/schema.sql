-- Installed only by render-demo, never by production migrations.
-- SECURITY DEFINER functions have fixed search paths and no PUBLIC execution.
CREATE SCHEMA IF NOT EXISTS vault_demo;
REVOKE ALL ON SCHEMA vault_demo FROM PUBLIC;
CREATE TABLE IF NOT EXISTS vault_demo.capacity (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 used_bytes bigint NOT NULL DEFAULT 0 CHECK (used_bytes BETWEEN 0 AND 100000000),
 object_count integer NOT NULL DEFAULT 0 CHECK (object_count BETWEEN 0 AND 10000)
);
INSERT INTO vault_demo.capacity(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS vault_demo.objects (
 storage_key text PRIMARY KEY CHECK (storage_key ~ '^blob-[0-9a-f]{64}$'),
 content bytea NOT NULL CHECK (octet_length(content) BETWEEN 0 AND 10000000),
 sha256 bytea NOT NULL CHECK (octet_length(sha256)=32 AND sha256=pg_catalog.sha256(content)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON ALL TABLES IN SCHEMA vault_demo FROM PUBLIC, vault_runtime, vault_gc;
GRANT USAGE ON SCHEMA vault_demo TO vault_runtime, vault_gc;
GRANT SELECT ON vault_demo.objects TO vault_runtime;

CREATE OR REPLACE FUNCTION vault_demo.put_object(p_key text, p_content bytea, p_hash bytea)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, vault_demo AS $$
DECLARE current_bytes bigint; current_count integer; size integer;
BEGIN
 size := octet_length(p_content);
 IF size IS NULL OR size>10000000 OR p_key IS NULL OR p_hash IS NULL
    OR p_key !~ '^blob-[0-9a-f]{64}$' OR p_hash<>pg_catalog.sha256(p_content) THEN
  RAISE EXCEPTION 'invalid demo object' USING ERRCODE='22023';
 END IF;
 -- A committed intent must precede bytes, just like immutable disk publication.
 -- Do not row-lock it: the publisher holds that row in a separate transaction.
 IF NOT EXISTS (SELECT 1 FROM vault.object_candidates
    WHERE storage_key=p_key AND state='PENDING' AND size_bytes=size AND sha256=p_hash) THEN
  RAISE EXCEPTION 'demo intent missing' USING ERRCODE='22023';
 END IF;
 SELECT used_bytes,object_count INTO STRICT current_bytes,current_count
 FROM vault_demo.capacity WHERE singleton FOR UPDATE;
 -- Leave headroom for metadata, indexes, WAL and dead tuples. This is admission
 -- protection, not a promise that other database activity cannot exhaust 1 GB.
 IF size>100000000-current_bytes OR current_count>=10000
    OR pg_database_size(current_database())>=650000000 THEN
  RAISE EXCEPTION 'demo storage capacity reached' USING ERRCODE='PV001';
 END IF;
 -- No UPSERT: an existing generation can never be overwritten.
 INSERT INTO vault_demo.objects(storage_key,content,sha256) VALUES(p_key,p_content,p_hash);
 UPDATE vault_demo.capacity SET used_bytes=used_bytes+size,object_count=object_count+1 WHERE singleton;
END $$;

CREATE OR REPLACE FUNCTION vault_demo.remove_object(p_key text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, vault_demo AS $$
DECLARE removed_size integer;
BEGIN
 IF p_key IS NULL OR p_key !~ '^blob-[0-9a-f]{64}$' THEN
  RAISE EXCEPTION 'invalid demo object' USING ERRCODE='22023';
 END IF;
 -- Recheck the durable fence; runtime has no delete capability.
 IF NOT EXISTS (SELECT 1 FROM vault.object_candidates WHERE storage_key=p_key AND state='DELETING')
    OR EXISTS (SELECT 1 FROM vault.blobs WHERE storage_key=p_key) THEN
  RAISE EXCEPTION 'demo object is not fenced' USING ERRCODE='22023';
 END IF;
 PERFORM 1 FROM vault_demo.capacity WHERE singleton FOR UPDATE;
 DELETE FROM vault_demo.objects WHERE storage_key=p_key RETURNING octet_length(content) INTO removed_size;
 IF FOUND THEN
  UPDATE vault_demo.capacity SET used_bytes=used_bytes-removed_size,object_count=object_count-1 WHERE singleton;
 END IF;
END $$;
REVOKE ALL ON FUNCTION vault_demo.put_object(text,bytea,bytea) FROM PUBLIC, vault_runtime, vault_gc;
REVOKE ALL ON FUNCTION vault_demo.remove_object(text) FROM PUBLIC, vault_runtime, vault_gc;
GRANT EXECUTE ON FUNCTION vault_demo.put_object(text,bytea,bytea) TO vault_runtime;
GRANT EXECUTE ON FUNCTION vault_demo.remove_object(text) TO vault_gc;
