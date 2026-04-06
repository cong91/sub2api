-- Add bootstrap balance support for invitation codes and seed default invitation balance setting.
-- This migration is idempotent.

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS bootstrap_balance decimal(20,8);

INSERT INTO settings (key, value, created_at, updated_at)
SELECT 'default_invitation_balance', COALESCE((SELECT value FROM settings WHERE key = 'default_balance' LIMIT 1), '0'), NOW(), NOW()
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'default_invitation_balance'
);
