//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIImagesUnsupportedItemFailoverExcludesSameProvider(t *testing.T) {
	groupID := int64(46)
	selected := openAIImageFailoverAccount(3737, "https://relay-a.example/v1")
	accounts := []Account{
		selected,
		openAIImageFailoverAccount(3738, "https://relay-a.example/v1/"),
		openAIImageFailoverAccount(3739, "https://relay-a.example/tenant/v1"),
		{ID: 3740, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{ID: 3741, Platform: PlatformGemini, Type: AccountTypeAPIKey},
	}
	svc := &OpenAIGatewayService{accountRepo: stubOpenAIAccountRepo{accounts: accounts}}
	prior := map[int64]struct{}{4000: {}}

	got, err := svc.OpenAIImagesUnsupportedItemFailoverAccountIDs(context.Background(), &groupID, &selected, prior)
	require.NoError(t, err)
	require.Equal(t, map[int64]struct{}{3737: {}, 3738: {}, 4000: {}}, got)
}

func TestOpenAIImagesUnsupportedItemFailoverRequiresProviderIdentity(t *testing.T) {
	groupID := int64(46)
	selected := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "not a URL"}}
	svc := &OpenAIGatewayService{accountRepo: stubOpenAIAccountRepo{accounts: []Account{selected}}}

	_, err := svc.OpenAIImagesUnsupportedItemFailoverAccountIDs(context.Background(), &groupID, &selected, nil)
	require.Error(t, err)
}

func openAIImageFailoverAccount(id int64, baseURL string) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": baseURL}}
}
