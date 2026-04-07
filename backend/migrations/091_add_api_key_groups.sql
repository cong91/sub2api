-- Add api_key_groups join table for multi-group API key grants.
CREATE TABLE IF NOT EXISTS api_key_groups (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, api_key_id)
);

CREATE INDEX IF NOT EXISTS idx_api_key_groups_api_key_id ON api_key_groups(api_key_id);

-- Backfill existing legacy single-group bindings.
INSERT INTO api_key_groups (api_key_id, group_id)
SELECT id, group_id
FROM api_keys
WHERE group_id IS NOT NULL
ON CONFLICT (group_id, api_key_id) DO NOTHING;
