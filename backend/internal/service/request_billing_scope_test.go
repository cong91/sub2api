package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyEffectiveGroup_DoesNotFallbackToLegacyGroup(t *testing.T) {
	legacyID := int64(999)
	apiKey := &APIKey{
		GroupID: &legacyID,
		Group: &Group{
			ID:       legacyID,
			Status:   StatusActive,
			Platform: PlatformOpenAI,
			Hydrated: true,
		},
	}

	require.Nil(t, apiKey.EffectiveGroup())
}

func TestResolveRequestBillingScope_FailsWhenEffectiveGroupMissing(t *testing.T) {
	legacyID := int64(123)
	apiKey := &APIKey{
		GroupID: &legacyID,
		Group: &Group{
			ID:       legacyID,
			Status:   StatusActive,
			Platform: PlatformAnthropic,
			Hydrated: true,
		},
	}

	scope, err := ResolveRequestBillingScope(apiKey, nil)
	require.Nil(t, scope)
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROUP_NOT_ASSIGNED")
}

func TestResolveRequestBillingScope_UsesGrantedEffectiveGroup(t *testing.T) {
	grantedID := int64(456)
	apiKey := &APIKey{
		GrantedGroups: []*Group{{
			ID:       grantedID,
			Status:   StatusActive,
			Platform: PlatformAnthropic,
			Hydrated: true,
		}},
	}

	scope, err := ResolveRequestBillingScope(apiKey, nil)
	require.NoError(t, err)
	require.NotNil(t, scope)
	require.NotNil(t, scope.BillingGroup)
	require.NotNil(t, scope.BillingGroupID)
	require.Equal(t, grantedID, *scope.BillingGroupID)
	require.Equal(t, grantedID, scope.BillingGroup.ID)
	require.Equal(t, BillingTypeBalance, scope.BillingType)
}
