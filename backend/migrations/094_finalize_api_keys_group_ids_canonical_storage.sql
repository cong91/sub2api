-- Finalize and verify canonical API key group membership storage.
-- This migration preserves migration immutability by keeping 093 unchanged
-- and applies only forward-safe canonical verification/cleanup here.
--
-- Goals:
--   1) api_keys.group_ids remains the only membership storage
--   2) legacy membership storage must not remain in schema
--   3) canonical invariants stay enforced across environments

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS group_ids BIGINT[];

UPDATE api_keys
SET group_ids = ARRAY[]::BIGINT[]
WHERE group_ids IS NULL;

ALTER TABLE api_keys
    ALTER COLUMN group_ids SET DEFAULT ARRAY[]::BIGINT[],
    ALTER COLUMN group_ids SET NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'api_keys'
          AND column_name = 'group_id'
    ) THEN
        ALTER TABLE api_keys DROP COLUMN group_id;
    END IF;
END
$$;

DROP INDEX IF EXISTS idx_api_key_groups_api_key_id;
DROP TABLE IF EXISTS api_key_groups;
DROP INDEX IF EXISTS api_keys_group_id;

CREATE INDEX IF NOT EXISTS idx_api_keys_group_ids_gin ON api_keys USING GIN (group_ids);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM api_keys
        WHERE cardinality(group_ids) = 0
    ) THEN
        RAISE EXCEPTION '094_finalize_api_keys_group_ids_canonical_storage: found api_keys rows with empty group_ids';
    END IF;
END
$$;
