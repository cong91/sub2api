//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImagesUnsupportedItemFailoverOAuth(t *testing.T) {
	responseBody := `{"error":{"message":"images contains an unsupported item","type":"invalid_request_error"}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(responseBody)),
	}}
	c, _ := newOpenAIImagesTestContext(t, nil)
	svc := newOpenAIImagesTestService(upstream)
	account := &Account{ID: 52, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "fixture-token"}}
	parsed := &OpenAIImagesRequest{
		Model: "gpt-image-2", Endpoint: openAIImagesEditsEndpoint, Prompt: "edit fixture", Multipart: true, N: 1,
		Uploads: []OpenAIImagesUpload{{FileName: "input.png", ContentType: "image/png", Data: []byte("fixture-image")}},
	}

	_, err := svc.ForwardImages(context.Background(), c, account, nil, parsed, "")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, OpenAIImagesUnsupportedItemReason, failoverErr.Reason)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.False(t, c.Writer.Written())
}

func TestOpenAIImagesUnsupportedItemFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, endpoint, response string
		status                   int
		wantFailover             bool
	}{
		{"exact edits error", openAIImagesEditsEndpoint, `{"error":{"message":"images contains an unsupported item","type":"invalid_request_error"}}`, 400, true},
		{"other endpoint", openAIImagesGenerationsEndpoint, `{"error":{"message":"images contains an unsupported item","type":"invalid_request_error"}}`, 400, false},
		{"other message", openAIImagesEditsEndpoint, `{"error":{"message":"invalid image","type":"invalid_request_error"}}`, 400, false},
		{"wrong error type", openAIImagesEditsEndpoint, `{"error":{"message":"images contains an unsupported item","type":"policy_error"}}`, 400, false},
		{"echoed request", openAIImagesEditsEndpoint, `{"error":{"message":"invalid image","type":"invalid_request_error"},"request":{"error":{"message":"images contains an unsupported item","type":"invalid_request_error"}}}`, 400, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2","prompt":"cat"}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/"+tc.endpoint, bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(tc.response))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 51, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "[REDACTED]"}}
			parsed := &OpenAIImagesRequest{Model: "gpt-image-2", Endpoint: tc.endpoint, ContentType: "application/json", N: 1}
			_, err := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
			require.Error(t, err)
			var failoverErr *UpstreamFailoverError
			if tc.wantFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.False(t, failoverErr.RetryableOnSameAccount)
				require.True(t, failoverErr.ShouldRetryNextAccount())
				require.False(t, c.Writer.Written())
				require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
				require.Equal(t, GatewayFailureScopeProvider, failoverErr.Scope)
				require.Equal(t, OpenAIImagesUnsupportedItemReason, failoverErr.Reason)
			} else {
				require.NotErrorAs(t, err, &failoverErr)
			}
		})
	}
}
