-- Explicit data migration cutover for API keys multi-group binding.
-- Canonical storage becomes api_keys.group_ids bigint[] only.
-- This migration intentionally performs data migration before removing legacy schema.
-- It is not a runtime fallback/backward-compatibility path.

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS group_ids BIGINT[];

-- 1) Build canonical group_ids from both legacy sources:
--    - api_key_groups join table
--    - api_keys.group_id scalar field
-- 2) Deduplicate + sort for deterministic ordering.
WITH migrated AS (
    SELECT
        ak.id AS api_key_id,
        ARRAY(
            SELECT DISTINCT gid
            FROM (
                SELECT ak.group_id AS gid
                UNION ALL
                SELECT akg.group_id AS gid
                FROM api_key_groups akg
                WHERE akg.api_key_id = ak.id
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

-- Ensure non-null array shape before enforcing constraints.
UPDATE api_keys
SET group_ids = ARRAY[]::BIGINT[]
WHERE group_ids IS NULL;

-- Verification guard: after migration every key must have at least one group.
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

-- Legacy schema is removed only after explicit data migration + verification.
DROP INDEX IF EXISTS idx_api_key_groups_api_key_id;
DROP TABLE IF EXISTS api_key_groups;

DROP INDEX IF EXISTS api_keys_group_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS group_id;

CREATE INDEX IF NOT EXISTS idx_api_keys_group_ids_gin ON api_keys USING GIN (group_ids);
