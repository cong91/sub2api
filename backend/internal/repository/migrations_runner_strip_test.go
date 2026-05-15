package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripRedundantTransactionControl(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strips standalone BEGIN/COMMIT",
			input:    "BEGIN;\nCREATE TABLE foo (id INT);\nCOMMIT;",
			expected: "CREATE TABLE foo (id INT);",
		},
		{
			name:     "strips standalone BEGIN without semicolon",
			input:    "BEGIN\nALTER TABLE foo ADD COLUMN bar TEXT;\nCOMMIT",
			expected: "ALTER TABLE foo ADD COLUMN bar TEXT;",
		},
		{
			name: "preserves BEGIN inside DO $$ block",
			input: `-- compat migration
DO $$
BEGIN
    IF to_regclass('public.user_allowed_groups') IS NULL THEN
        ALTER TABLE users ADD COLUMN IF NOT EXISTS allowed_groups BIGINT[] DEFAULT NULL;
    END IF;
END $$;`,
			expected: `-- compat migration
DO $$
BEGIN
    IF to_regclass('public.user_allowed_groups') IS NULL THEN
        ALTER TABLE users ADD COLUMN IF NOT EXISTS allowed_groups BIGINT[] DEFAULT NULL;
    END IF;
END $$;`,
		},
		{
			name: "preserves BEGIN inside DO $$ with nested IF",
			input: `DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'users') THEN
        IF to_regclass('public.user_allowed_groups') IS NULL THEN
            ALTER TABLE users ADD COLUMN IF NOT EXISTS allowed_groups BIGINT[] DEFAULT NULL;
        END IF;
    END IF;
END $$;`,
			expected: `DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'users') THEN
        IF to_regclass('public.user_allowed_groups') IS NULL THEN
            ALTER TABLE users ADD COLUMN IF NOT EXISTS allowed_groups BIGINT[] DEFAULT NULL;
        END IF;
    END IF;
END $$;`,
		},
		{
			name:     "no-op for plain SQL",
			input:    "ALTER TABLE users ADD COLUMN name TEXT;",
			expected: "ALTER TABLE users ADD COLUMN name TEXT;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripRedundantTransactionControl(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
