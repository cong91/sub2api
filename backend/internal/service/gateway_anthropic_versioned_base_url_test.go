//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayServiceBuildUpstreamRequestVersionedBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GatewayService{cfg: &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}}
	account := &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "upstream-key",
			"base_url": "https://relay.example.com/v1",
		},
	}

	req, _, err := svc.buildUpstreamRequest(
		context.Background(), c, account,
		[]byte(`{"model":"claude-sonnet-4-5","messages":[]}`),
		"upstream-key", "apikey", "claude-sonnet-4-5", false, false,
	)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.com/v1/messages?beta=true", req.URL.String())
}

func TestBuildUpstreamRequestAnthropicAPIKeyPassthroughVersionedBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GatewayService{cfg: &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}}
	account := &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "upstream-key",
			"base_url": "https://relay.example.com/v1/",
		},
		Extra: map[string]any{"anthropic_passthrough": true},
	}

	req, _, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(
		context.Background(), c, account,
		[]byte(`{"model":"claude-sonnet-4-5","messages":[]}`),
		"upstream-key",
	)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.com/v1/messages?beta=true", req.URL.String())
}

func TestBuildCountTokensRequestAnthropicAPIKeyPassthroughPreservesBaseQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	svc := &GatewayService{cfg: &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}}
	account := &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "upstream-key",
			"base_url": "https://relay.example.com/anthropic/v1?tenant=acme",
		},
	}

	req, err := svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(
		context.Background(), c, account,
		[]byte(`{"model":"claude-sonnet-4-5","messages":[]}`),
		"upstream-key",
	)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.com/anthropic/v1/messages/count_tokens?beta=true&tenant=acme", req.URL.String())
}

func TestBuildCustomRelayURLPreservesBaseQueryAndAddsProxy(t *testing.T) {
	proxyID := int64(7)
	account := &Account{
		ProxyID: &proxyID,
		Proxy: &Proxy{
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     8080,
		},
	}

	got := (&GatewayService{}).buildCustomRelayURL(
		"https://relay.example.com/v1?tenant=acme",
		"/v1/messages",
		account,
	)
	parsed, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "/v1/messages", parsed.Path)
	require.Equal(t, "acme", parsed.Query().Get("tenant"))
	require.Equal(t, "true", parsed.Query().Get("beta"))
	require.Equal(t, "http://127.0.0.1:8080", parsed.Query().Get("proxy"))
}
