//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type blockingReadCloser struct {
	lines []string
	idx   int
	pos   int
	block <-chan struct{}
}

func (b *blockingReadCloser) Read(p []byte) (int, error) {
	for {
		if b.idx < len(b.lines) {
			line := b.lines[b.idx]
			if b.pos >= len(line) {
				b.idx++
				b.pos = 0
				continue
			}
			n := copy(p, line[b.pos:])
			b.pos += n
			if b.pos >= len(line) {
				b.idx++
				b.pos = 0
			}
			return n, nil
		}

		<-b.block
		return 0, io.EOF
	}
}

func (b *blockingReadCloser) Close() error { return nil }

func TestExtractCCReasoningEffortFromBody(t *testing.T) {
	t.Parallel()

	t.Run("nested reasoning.effort", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning":{"effort":"HIGH"}}`))
		require.NotNil(t, got)
		require.Equal(t, "high", *got)
	})

	t.Run("flat reasoning_effort", func(t *testing.T) {
		got := extractCCReasoningEffortFromBody([]byte(`{"reasoning_effort":"x-high"}`))
		require.NotNil(t, got)
		require.Equal(t, "xhigh", *got)
	})

	t.Run("missing effort", func(t *testing.T) {
		require.Nil(t, extractCCReasoningEffortFromBody([]byte(`{"model":"gpt-5"}`)))
	})
}

func TestHandleCCBufferedFromAnthropic_PreservesMessageStartCacheUsageAndReasoning(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	reasoningEffort := "high"
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_buffered"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCBufferedFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", &reasoningEffort, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
}

func TestHandleCCStreamingFromAnthropic_PreservesMessageStartCacheUsageAndReasoning(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	reasoningEffort := "medium"
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4.5","stop_reason":"","usage":{"input_tokens":20,"cache_read_input_tokens":11,"cache_creation_input_tokens":4}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"hello"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	svc := &GatewayService{}
	result, err := svc.handleCCStreamingFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", &reasoningEffort, time.Now(), true)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 20, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 11, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.CacheCreationInputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "medium", *result.ReasoningEffort)
	require.Contains(t, rec.Body.String(), `[DONE]`)
}

func TestHandleCCBufferedFromAnthropic_ReturnsOnTerminalEventWithoutUpstreamClose(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	block := make(chan struct{})
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_buffered_terminal"}},
		Body: &blockingReadCloser{
			lines: []string{
				"event: message_start\n",
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_buf_term\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4.5\",\"usage\":{\"input_tokens\":4}}}\n",
				"\n",
				"event: content_block_start\n",
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"hi\"}}\n",
				"\n",
				"event: message_delta\n",
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n",
				"\n",
			},
			block: block,
		},
	}

	svc := &GatewayService{}
	type callResult struct {
		result *ForwardResult
		err    error
	}
	resultCh := make(chan callResult, 1)
	go func() {
		result, err := svc.handleCCBufferedFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now())
		resultCh <- callResult{result: result, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 4, got.result.Usage.InputTokens)
		require.Equal(t, 2, got.result.Usage.OutputTokens)
	case <-ctx.Done():
		close(block)
		t.Fatal("handleCCBufferedFromAnthropic did not return after terminal event")
	}
}

func TestHandleCCStreamingFromAnthropic_ReturnsOnTerminalEventWithoutUpstreamClose(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	block := make(chan struct{})
	resp := &http.Response{
		Header: http.Header{"x-request-id": []string{"rid_cc_stream_terminal"}},
		Body: &blockingReadCloser{
			lines: []string{
				"event: message_start\n",
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_stream_term\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4.5\",\"usage\":{\"input_tokens\":5}}}\n",
				"\n",
				"event: content_block_start\n",
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"hello\"}}\n",
				"\n",
				"event: message_stop\n",
				"data: {\"type\":\"message_stop\"}\n",
				"\n",
			},
			block: block,
		},
	}

	svc := &GatewayService{}
	type callResult struct {
		result *ForwardResult
		err    error
	}
	resultCh := make(chan callResult, 1)
	go func() {
		result, err := svc.handleCCStreamingFromAnthropic(resp, c, "gpt-5", "claude-sonnet-4.5", nil, time.Now(), true)
		resultCh <- callResult{result: result, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 5, got.result.Usage.InputTokens)
		require.Contains(t, rec.Body.String(), `[DONE]`)
	case <-ctx.Done():
		close(block)
		t.Fatal("handleCCStreamingFromAnthropic did not return after terminal event")
	}
}
