-- Migration: Fix rate_multiplier for balance package groups
-- 
-- Previously rate_multiplier was set to 0.175 for all groups, which is incorrect.
-- The correct values are computed from:
--   rate_multiplier = amount_ledger / (actual_credits / 1M × avg_price_per_1M_tokens)
-- Where avg_price_per_1M_tokens = $5.625 (GPT-5.5 weighted avg, 3:1 input:output ratio)
--
-- Package     | Amount | Tokens | Correct rate_multiplier
-- standard    | $7.34  | 27M    | 0.048329
-- pro         | $14.68 | 63M    | 0.041425
-- expert      | $29.37 | 130M   | 0.040164
-- business    | $73.42 | 340M   | 0.038390
-- enterprise  | $146.84| 700M   | 0.037293

-- Update group rate_multipliers (groups 14-18 correspond to balance packages)
UPDATE groups SET rate_multiplier = 0.048329 WHERE id = 14; -- Standard
UPDATE groups SET rate_multiplier = 0.041425 WHERE id = 15; -- Pro
UPDATE groups SET rate_multiplier = 0.040164 WHERE id = 16; -- Expert
UPDATE groups SET rate_multiplier = 0.038390 WHERE id = 17; -- Business
UPDATE groups SET rate_multiplier = 0.037293 WHERE id = 18; -- Enterprise

-- Also update balance_packages to reflect the simplified model:
-- credit_ledger = amount_ledger (no more inflated balance)
-- bonus_ledger = 0 (no more fake bonus)
-- credit_multiplier = 1 (no more multiplier)
UPDATE balance_packages SET credit_ledger = amount_ledger, bonus_ledger = 0, credit_multiplier = 1;
