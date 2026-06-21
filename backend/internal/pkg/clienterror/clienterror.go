// Package clienterror normalizes user-facing error messages.
package clienterror

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Message returns an English-only message suitable for API clients.
// It preserves existing English messages but replaces CJK/localized messages
// with a known English translation or a status-appropriate local generic
// message.
func Message(status int, message string) string {
	return MessageWithFallback(status, message, DefaultMessage(status))
}

// UpstreamMessage is like Message, but unknown localized upstream text falls
// back to an upstream-oriented message rather than a local application one.
func UpstreamMessage(status int, message string) string {
	return MessageWithFallback(status, message, DefaultUpstreamMessage(status))
}

// MessageWithFallback normalizes message to English and uses fallback when the
// original text is empty or localized but not in the known translation table.
func MessageWithFallback(status int, message string, fallback string) string {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return fallbackOrDefault(status, fallback)
	}
	if translated := translateKnownMessage(msg); translated != "" {
		return translated
	}
	if ContainsCJK(msg) {
		return fallbackOrDefault(status, fallback)
	}
	return msg
}

// DefaultMessage returns a generic local-application English message for the
// HTTP status.
func DefaultMessage(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "Invalid request"
	case http.StatusUnauthorized:
		return "Authentication failed"
	case http.StatusForbidden:
		return "Access forbidden"
	case http.StatusNotFound:
		return "Resource not found"
	case http.StatusRequestEntityTooLarge:
		return "Request body is too large"
	case http.StatusTooManyRequests:
		return "Rate limit exceeded, please retry later"
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return "Request timed out"
	case http.StatusServiceUnavailable, 529:
		return "Service temporarily unavailable"
	case http.StatusBadGateway:
		return "Upstream request failed"
	default:
		if status >= 500 {
			return "Internal server error"
		}
		return "Request failed"
	}
}

// DefaultUpstreamMessage returns a generic English message for errors whose
// source is an upstream provider or relay.
func DefaultUpstreamMessage(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "Invalid upstream request"
	case http.StatusUnauthorized:
		return "Upstream authentication failed, please contact administrator"
	case http.StatusForbidden:
		return "Upstream access forbidden, please contact administrator"
	case http.StatusNotFound:
		return "Upstream resource not found"
	case http.StatusRequestEntityTooLarge:
		return "Upstream request body is too large"
	case http.StatusTooManyRequests:
		return "Upstream rate limit exceeded, please retry later"
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return "Upstream request timed out"
	case http.StatusServiceUnavailable, 529:
		return "Upstream service temporarily unavailable"
	case http.StatusBadGateway:
		return "Upstream request failed"
	default:
		if status >= 500 {
			return "Upstream service temporarily unavailable"
		}
		return "Upstream request failed"
	}
}

// TypeForHTTPStatus returns an OpenAI/Anthropic-style error type for a status.
func TypeForHTTPStatus(status int, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return "timeout_error"
	case http.StatusServiceUnavailable, 529:
		return "overloaded_error"
	}
	if fallback != "" {
		return fallback
	}
	return "upstream_error"
}

// JSONBody rewrites common JSON error message fields to English. If the body is
// not JSON and contains CJK text, it returns a standard JSON error envelope.
func JSONBody(status int, body []byte, fallbackType, fallbackMessage string) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return buildJSON(status, fallbackType, fallbackMessage)
	}
	if !gjson.ValidBytes(trimmed) {
		if ContainsCJK(string(trimmed)) {
			return buildJSON(status, fallbackType, fallbackMessage)
		}
		return append([]byte(nil), body...)
	}

	out := append([]byte(nil), trimmed...)
	fallbackMessage = UpstreamMessage(status, fallbackMessage)
	for _, path := range []string{"error.message", "message", "detail", "error.detail"} {
		result := gjson.GetBytes(out, path)
		if !result.Exists() || result.Type != gjson.String {
			continue
		}
		normalized := MessageWithFallback(status, result.String(), fallbackMessage)
		if normalized != result.String() {
			var err error
			out, err = sjson.SetBytes(out, path, normalized)
			if err != nil {
				return buildJSON(status, fallbackType, fallbackMessage)
			}
		}
	}
	if errValue := gjson.GetBytes(out, "error"); errValue.Exists() && errValue.IsObject() && !gjson.GetBytes(out, "error.type").Exists() {
		var err error
		out, err = sjson.SetBytes(out, "error.type", TypeForHTTPStatus(status, fallbackType))
		if err != nil {
			return buildJSON(status, fallbackType, fallbackMessage)
		}
	}

	if errValue := gjson.GetBytes(out, "error"); errValue.Exists() && errValue.Type == gjson.String && ContainsCJK(errValue.String()) {
		return buildJSON(status, fallbackType, fallbackMessage)
	}
	if ContainsCJK(string(out)) {
		return buildJSON(status, fallbackType, fallbackMessage)
	}
	return out
}

// ContainsCJK reports whether s contains CJK characters likely to be non-English
// user-facing text. Vietnamese Latin diacritics are intentionally not matched.
func ContainsCJK(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return true
		}
	}
	return false
}

func buildJSON(status int, fallbackType, fallbackMessage string) []byte {
	errType := TypeForHTTPStatus(status, fallbackType)
	message := MessageWithFallback(status, fallbackMessage, DefaultUpstreamMessage(status))
	if message == "" {
		message = DefaultUpstreamMessage(status)
	}
	return []byte(fmt.Sprintf(`{"error":{"type":%q,"message":%q}}`, errType, message))
}

func fallbackOrDefault(status int, fallback string) string {
	if trimmed := strings.TrimSpace(fallback); trimmed != "" {
		if translated := translateKnownMessage(trimmed); translated != "" {
			return translated
		}
		if !ContainsCJK(trimmed) {
			return trimmed
		}
	}
	return DefaultMessage(status)
}

func translateKnownMessage(msg string) string {
	for _, item := range knownTranslations {
		if strings.Contains(msg, item.match) {
			return item.english
		}
	}
	return ""
}

var knownTranslations = []struct {
	match   string
	english string
}{
	{"请求格式或参数不正确", "Invalid request format or parameters"},
	{"参数无效", "Invalid request parameters"},
	{"参数不正确", "Invalid request parameters"},
	{"请求格式", "Invalid request format"},
	{"参数错误", "Invalid request parameters"},
	{"请求无效", "Invalid request"},
	{"上游请求失败", "Upstream request failed"},
	{"上游失败", "Upstream request failed"},
	{"上游服务暂时不可用", "Upstream service temporarily unavailable"},
	{"上游服务不可用", "Upstream service temporarily unavailable"},
	{"上游认证失败", "Upstream authentication failed, please contact administrator"},
	{"上游访问被拒绝", "Upstream access forbidden, please contact administrator"},
	{"请求体为空", "Request body is empty"},
	{"API Key 所属专属分组不再允许当前用户使用", "API key group is no longer available for this user"},
	{"API Key 所属分组已删除", "API key group has been deleted"},
	{"API Key 所属分组已停用", "API key group has been disabled"},
	{"API key 额度已用完", "API key quota exhausted"},
	{"api key 额度已用完", "API key quota exhausted"},
	{"API key 已过期", "API key has expired"},
	{"api key 已过期", "API key has expired"},
	{"余额不足", "Insufficient balance or quota"},
	{"额度不足", "Insufficient balance or quota"},
	{"上下文超限", "Context length exceeded"},
	{"上下文长度", "Context length exceeded"},
	{"超出上下文", "Context length exceeded"},
	{"未登录", "Authentication required"},
	{"无权限", "Access forbidden"},
	{"资源不存在", "Resource not found"},
	{"服务器内部错误", "Internal server error"},
}
