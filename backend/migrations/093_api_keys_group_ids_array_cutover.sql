-- Explicit data migration cutover for API keys multi-group binding.
-- Final approved canonical source is api_keys.group_ids bigint[] only.
-- This migration rewrites stored membership into api_keys.group_ids,
-- verifies the result, and then removes all legacy storage.

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS group_ids BIGINT[];

WITH migrated AS (
    SELECT
        ak.id AS api_key_id,
        ARRAY(
            SELECT DISTINCT gid
            FROM (
                SELECT ak.group_id AS gid
            ) merged
            WHERE gid IS NOT NULL
            ORDER BY gid
        ) AS resolved_group_ids
    FROM api_keys ak
)
UPDATE api_keys ak
SET group_ids = COALESCE(m.resolved_group_ids, ARRAY[]::BIGINT[])
FROM migrated m
WHERE ak.id = m.api_key_id;

UPDATE api_keys
SET group_ids = ARRAY[]::BIGINT[]
WHERE group_ids IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM api_keys
        WHERE cardinality(group_ids) = 0
    ) THEN
        RAISE EXCEPTION '093_api_keys_group_ids_array_cutover: found api_keys rows with empty group_ids after migration';
    END IF;
END
$$;

ALTER TABLE api_keys
    ALTER COLUMN group_ids SET DEFAULT ARRAY[]::BIGINT[],
    ALTER COLUMN group_ids SET NOT NULL;

DROP INDEX IF EXISTS idx_api_key_groups_api_key_id;
DROP TABLE IF EXISTS api_key_groups;

DROP INDEX IF EXISTS api_keys_group_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS group_id;

CREATE INDEX IF NOT EXISTS idx_api_keys_group_ids_gin ON api_keys USING GIN (group_ids);
