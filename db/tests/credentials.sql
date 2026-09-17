-- Run using psql -v ON_ERROR_STOP=1 as the migration/admin role. All data rolls back.
BEGIN;
DO $$
DECLARE
    owner UUID;
    second_owner UUID;
    test_login TEXT := 'test_' || replace(gen_random_uuid()::text, '-', '');
BEGIN
    INSERT INTO vault.users DEFAULT VALUES RETURNING id INTO owner;
    IF (SELECT role FROM vault.users WHERE id=owner) <> 'USER' THEN
        RAISE EXCEPTION 'unsafe default role';
    END IF;
    INSERT INTO vault.users(role) VALUES ('ADMIN') RETURNING id INTO second_owner;
    INSERT INTO vault.credentials(user_id,login_name,password_hash)
        VALUES (owner,test_login,repeat('x',100));
    BEGIN
        INSERT INTO vault.credentials(user_id,login_name,password_hash)
            VALUES (second_owner,test_login,repeat('x',100));
        RAISE EXCEPTION 'duplicate login accepted';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO vault.credentials(user_id,login_name,password_hash)
            VALUES (second_owner,'UPPERCASE',repeat('x',100));
        RAISE EXCEPTION 'noncanonical login accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        UPDATE vault.users SET role='SUPERUSER' WHERE id=owner;
        RAISE EXCEPTION 'invalid role accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    IF NOT has_table_privilege('vault_runtime','vault.credentials','SELECT')
       OR has_table_privilege('vault_runtime','vault.credentials','INSERT')
       OR has_table_privilege('vault_runtime','vault.credentials','UPDATE')
       OR has_table_privilege('vault_runtime','vault.credentials','DELETE')
       OR has_column_privilege('vault_runtime','vault.users','role','UPDATE') THEN
        RAISE EXCEPTION 'runtime privilege boundary incorrect';
    END IF;
END $$;
ROLLBACK;
