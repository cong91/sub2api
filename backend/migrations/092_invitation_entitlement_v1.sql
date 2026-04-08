-- Invite code entitlement v1
-- Add explicit invitation benefit columns while keeping legacy columns for compatibility.

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS benefit_type VARCHAR(20),
    ADD COLUMN IF NOT EXISTS balance_amount DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS subscription_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS subscription_days INTEGER;

CREATE INDEX IF NOT EXISTS idx_redeem_codes_subscription_group_id ON redeem_codes(subscription_group_id);

-- Backfill invitation benefit columns from legacy payload when possible.
UPDATE redeem_codes
SET
    benefit_type = CASE
        WHEN group_id IS NOT NULL AND validity_days > 0 AND value > 0 THEN 'subscription'
        WHEN group_id IS NOT NULL AND validity_days > 0 THEN 'subscription'
        WHEN value > 0 THEN 'balance'
        ELSE NULL
    END,
    balance_amount = CASE
        WHEN group_id IS NULL AND value > 0 THEN value
        ELSE NULL
    END,
    subscription_group_id = CASE
        WHEN group_id IS NOT NULL AND validity_days > 0 THEN group_id
        ELSE NULL
    END,
    subscription_days = CASE
        WHEN group_id IS NOT NULL AND validity_days > 0 THEN validity_days
        ELSE NULL
    END
WHERE type = 'invitation'
  AND benefit_type IS NULL;
