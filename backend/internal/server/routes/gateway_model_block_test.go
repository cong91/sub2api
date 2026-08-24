package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayRouteModelBlockRepo struct {
	service.UserRepository
	block service.UserModelBlock
}

func (r *gatewayRouteModelBlockRepo) ListUserModelBlocks(context.Context, int64) ([]service.UserModelBlock, error) {
	return []service.UserModelBlock{r.block}, nil
}

func (r *gatewayRouteModelBlockRepo) SetUserModelBlock(context.Context, int64, service.UserModelBlock, bool) error {
	return nil
}

func (r *gatewayRouteModelBlockRepo) IsUserModelBlocked(_ context.Context, userID int64, block service.UserModelBlock) (bool, error) {
	if userID <= 0 {
		return false, errors.New("missing user id")
	}
	return block == r.block, nil
}

func TestGatewayRoutesRejectBlockedModelBeforeHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxBodySize:     1024 * 1024,
		TextMaxBodySize: 1024 * 1024,
	}}
	blockRepo := &gatewayRouteModelBlockRepo{
		block: service.UserModelBlock{Platform: service.PlatformOpenAI, Model: "gpt-4.1"},
	}
	apiKeyService := service.NewAPIKeyService(nil, blockRepo, nil, nil, nil, nil, cfg)
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
			AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(1)
			userID := int64(42)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				UserID:  userID,
				GroupID: &groupID,
				User:    &service.User{ID: userID},
				Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI},
			})
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.UserID, userID))
			c.Next()
		}),
		apiKeyService,
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "MODEL_BLOCKED")
}
