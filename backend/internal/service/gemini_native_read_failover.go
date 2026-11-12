package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

func geminiNativeRequestCanceled(ctx context.Context, c *gin.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

// restoreGeminiNativeResponseHeaders removes headers from a failed pre-output
// upstream attempt while preserving headers installed by middleware.
func restoreGeminiNativeResponseHeaders(c *gin.Context, headers http.Header) {
	if c == nil || c.Writer == nil || c.Writer.Written() || IsResponseCommitted(c) {
		return
	}
	restoreGatewayResponseHeaders(c, headers)
}

// Gemini native body-read failures are retryable only before any bytes or
// headers have reached the client. Never expose transport diagnostics.
func newGeminiNativeReadFailoverError(ctx context.Context, c *gin.Context, resp *http.Response, readErr error) (*UpstreamFailoverError, bool) {
	if readErr == nil || errors.Is(readErr, io.EOF) ||
		errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) ||
		errors.Is(readErr, ErrUpstreamResponseBodyTooLarge) || errors.Is(readErr, bufio.ErrTooLong) {
		return nil, false
	}
	if geminiNativeRequestCanceled(ctx, c) {
		return nil, false
	}
	if c != nil && c.Writer != nil && (c.Writer.Written() || IsResponseCommitted(c)) {
		return nil, false
	}
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"code":    http.StatusBadGateway,
			"message": "Upstream connection error",
			"status":  "UNAVAILABLE",
		},
	})
	var headers http.Header
	if resp != nil {
		headers = resp.Header.Clone()
	}
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           body,
		ResponseHeaders:        headers,
		RetryableOnSameAccount: true,
	}, true
}
