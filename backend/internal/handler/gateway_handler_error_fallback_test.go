package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayEnsureForwardErrorResponse_WritesFallbackWhenNotWritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	h := &GatewayHandler{}
	wrote := h.ensureForwardErrorResponse(c, false)

	require.True(t, wrote)
	require.Equal(t, http.StatusBadGateway, w.Code)

	var parsed map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "error", parsed["type"])
	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upstream_error", errorObj["type"])
	assert.Equal(t, "Upstream request failed", errorObj["message"])
}

func TestGatewayEnsureForwardErrorResponse_DoesNotOverrideWrittenResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.String(http.StatusTeapot, "already written")

	h := &GatewayHandler{}
	wrote := h.ensureForwardErrorResponse(c, false)

	require.False(t, wrote)
	require.Equal(t, http.StatusTeapot, w.Code)
	assert.Equal(t, "already written", w.Body.String())
}

func TestGatewayProviderCatalog_UnauthorizedWithoutAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/provider-catalog", nil)

	h := &GatewayHandler{}
	h.ProviderCatalog(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGatewayProviderCatalog_OpenAIContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/provider-catalog", nil)

	apiKey := &service.APIKey{GrantedGroups: []*service.Group{{ID: 99, Platform: service.PlatformOpenAI, Hydrated: true}}}
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	h := &GatewayHandler{gatewayService: &service.GatewayService{}}
	h.ProviderCatalog(c)

	require.Equal(t, http.StatusOK, w.Code)

	var payload struct {
		Object    string `json:"object"`
		Providers []struct {
			ProviderID   string `json:"provider_id"`
			ProviderName string `json:"provider_name"`
			APIStyle     string `json:"api_style"`
			DefaultModel string `json:"default_model"`
			Models       []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "provider_catalog", payload.Object)
	require.NotEmpty(t, payload.Providers)
	require.Equal(t, "openai", payload.Providers[0].ProviderID)
	require.NotEmpty(t, payload.Providers[0].ProviderName)
	require.NotEmpty(t, payload.Providers[0].APIStyle)
	require.NotEmpty(t, payload.Providers[0].DefaultModel)
	require.NotEmpty(t, payload.Providers[0].Models)
}

func TestGatewayProviderCatalog_MultiGroupAggregatesProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/provider-catalog", nil)

	apiKey := &service.APIKey{
		GrantedGroups: []*service.Group{
			{ID: 99, Platform: service.PlatformOpenAI, Hydrated: true},
			{ID: 100, Platform: service.PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude", "gemini_text", "gemini_image"}},
		},
	}
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	h := &GatewayHandler{gatewayService: &service.GatewayService{}}
	h.ProviderCatalog(c)

	require.Equal(t, http.StatusOK, w.Code)

	var payload struct {
		Object    string `json:"object"`
		Providers []struct {
			ProviderID string `json:"provider_id"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "provider_catalog", payload.Object)
	require.Len(t, payload.Providers, 3)

	ids := map[string]struct{}{}
	for _, p := range payload.Providers {
		ids[p.ProviderID] = struct{}{}
	}
	_, hasOpenAI := ids[service.PlatformOpenAI]
	_, hasAnthropic := ids[service.PlatformAnthropic]
	_, hasGemini := ids[service.PlatformGemini]
	require.True(t, hasOpenAI)
	require.True(t, hasAnthropic)
	require.True(t, hasGemini)
}

func TestGatewayProviderCatalog_UsesGrantedGroupUnionFromRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/provider-catalog", nil)

	apiKey := &service.APIKey{
		GrantedGroups: []*service.Group{
			{ID: 99, Platform: service.PlatformOpenAI, Hydrated: true},
		},
	}
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	ctxGranted := []*service.Group{
		{ID: 99, Platform: service.PlatformOpenAI, Hydrated: true},
		{ID: 100, Platform: service.PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude", "gemini_text", "gemini_image"}},
	}
	ctx := context.WithValue(c.Request.Context(), ctxkey.GrantedGroups, ctxGranted)
	c.Request = c.Request.WithContext(ctx)

	h := &GatewayHandler{gatewayService: &service.GatewayService{}}
	h.ProviderCatalog(c)

	require.Equal(t, http.StatusOK, w.Code)

	var payload struct {
		Object    string `json:"object"`
		Providers []struct {
			ProviderID string `json:"provider_id"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "provider_catalog", payload.Object)
	require.Len(t, payload.Providers, 3)

	ids := map[string]struct{}{}
	for _, p := range payload.Providers {
		ids[p.ProviderID] = struct{}{}
	}
	_, hasOpenAI := ids[service.PlatformOpenAI]
	_, hasAnthropic := ids[service.PlatformAnthropic]
	_, hasGemini := ids[service.PlatformGemini]
	require.True(t, hasOpenAI)
	require.True(t, hasAnthropic)
	require.True(t, hasGemini)
}
