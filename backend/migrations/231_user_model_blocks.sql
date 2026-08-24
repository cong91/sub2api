-- Persist exact user-level model blocks. This is additive and safe to replay.
CREATE TABLE IF NOT EXISTS user_model_blocks (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform   VARCHAR(50) NOT NULL,
    model      VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, platform, model)
);
