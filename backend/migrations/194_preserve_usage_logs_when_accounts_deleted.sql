-- Historical usage logs are billing and audit records. Removing an upstream
-- account must not remove those records; keep the account id nullable after
-- the account is deleted so all aggregate statistics remain available.
ALTER TABLE usage_logs
    ALTER COLUMN account_id DROP NOT NULL;

DO $$
DECLARE
    constraint_name text;
BEGIN
    SELECT tc.constraint_name
      INTO constraint_name
      FROM information_schema.table_constraints tc
      JOIN information_schema.key_column_usage kcu
        ON tc.constraint_name = kcu.constraint_name
       AND tc.table_schema = kcu.table_schema
     WHERE tc.table_schema = current_schema()
       AND tc.table_name = 'usage_logs'
       AND tc.constraint_type = 'FOREIGN KEY'
       AND kcu.column_name = 'account_id'
     LIMIT 1;

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE usage_logs DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;
