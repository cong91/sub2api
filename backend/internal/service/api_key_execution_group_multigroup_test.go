//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestAPIKey_ExecutionGroupResolver_MultiGroupOpenAILaneSelectsOpenAIGroup(t *testing.T) {
	apiKey := &APIKey{
		GroupIDs: []int64{2, 3},
		Groups: []*Group{
			{ID: 3, Name: "antigravity", Platform: PlatformAntigravity, Status: StatusActive, Hydrated: true},
			{ID: 2, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		},
	}

	ctx := context.WithValue(context.Background(), ctxkey.Platform, PlatformOpenAI)
	group := apiKey.ExecutionGroupResolver(ctx)
	require.NotNil(t, group)
	require.Equal(t, int64(2), group.ID)
	require.Equal(t, PlatformOpenAI, group.Platform)
}

func TestAPIKey_ExecutionGroupResolver_MultiGroupAntigravityLaneSelectsAntigravityGroup(t *testing.T) {
	apiKey := &APIKey{
		GroupIDs: []int64{2, 3},
		Groups: []*Group{
			{ID: 2, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
			{ID: 3, Name: "antigravity", Platform: PlatformAntigravity, Status: StatusActive, Hydrated: true},
		},
	}

	ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAntigravity)
	group := apiKey.ExecutionGroupResolver(ctx)
	require.NotNil(t, group)
	require.Equal(t, int64(3), group.ID)
	require.Equal(t, PlatformAntigravity, group.Platform)
}

func TestAPIKey_ExecutionGroupResolver_MultiGroupGeminiLaneCanUseAntigravityBackedGroup(t *testing.T) {
	apiKey := &APIKey{
		GroupIDs: []int64{2, 3},
		Groups: []*Group{
			{ID: 2, Name: "openai", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
			{ID: 3, Name: "antigravity", Platform: PlatformAntigravity, Status: StatusActive, Hydrated: true},
		},
	}

	ctx := context.WithValue(context.Background(), ctxkey.Platform, PlatformGemini)
	group := apiKey.ExecutionGroupResolver(ctx)
	require.NotNil(t, group)
	require.Equal(t, int64(3), group.ID)
	require.Equal(t, PlatformAntigravity, group.Platform)
}
