package service

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateRedeemCodeForType_UsesExpectedPrefixesAndFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		codeType string
		pattern  string
	}{
		{name: "balance", codeType: RedeemTypeBalance, pattern: `^BAL-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`},
		{name: "subscription", codeType: RedeemTypeSubscription, pattern: `^SUB-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`},
		{name: "invitation", codeType: RedeemTypeInvitation, pattern: `^INV-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`},
		{name: "concurrency", codeType: RedeemTypeConcurrency, pattern: `^CON-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`},
		{name: "admin balance adjustment", codeType: AdjustmentTypeAdminBalance, pattern: `^BAL-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`},
		{name: "admin concurrency adjustment", codeType: AdjustmentTypeAdminConcurrency, pattern: `^CON-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, err := GenerateRedeemCodeForType(tt.codeType)
			require.NoError(t, err)
			require.Regexp(t, regexp.MustCompile(tt.pattern), code)
		})
	}
}

func TestGenerateRedeemCodeForType_UnknownTypeReturnsError(t *testing.T) {
	t.Parallel()

	code, err := GenerateRedeemCodeForType("mystery")
	require.Error(t, err)
	require.Empty(t, code)
}

func TestNormalizeRedeemCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, "BAL-CGBA-WX9R-N1PA", NormalizeRedeemCode("  bal-cgba-wx9r-n1pa  "))
	require.Equal(t, "SUB-ABCD-EF12-GH34", NormalizeRedeemCode("sub-abcd-ef12-gh34"))
	require.Equal(t, "", NormalizeRedeemCode("   "))
}
