//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApiKeyAuthWithSubscriptionGoogle_MultiGroupAntigravityBackedGeminiLaneSetsExecutionGroupContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	openAIGroup := &service.Group{ID: 2, Name: "openai", Status: service.StatusActive, Platform: service.PlatformOpenAI, Hydrated: true}
	antigravityGroup := &service.Group{ID: 3, Name: "antigravity", Status: service.StatusActive, Platform: service.PlatformAntigravity, Hydrated: true}
	user := &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 10, Concurrency: 3}
	apiKey := &service.APIKey{
		ID:       100,
		UserID:   user.ID,
		Key:      "multi-group-google-key",
		Status:   service.StatusActive,
		User:     user,
		GroupIDs: []int64{2, 3},
		Groups:   []*service.Group{antigravityGroup, openAIGroup},
	}

	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*service.APIKey, error) {
			if key != apiKey.Key {
				return nil, service.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	})

	cfg := &config.Config{RunMode: config.RunModeSimple}
	r := gin.New()
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) {
		groupFromCtx, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group)
		if !ok || groupFromCtx == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		if groupFromCtx.ID != antigravityGroup.ID || groupFromCtx.Platform != antigravityGroup.Platform {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Platform, service.PlatformGemini))
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
