package service

import (
	"net/url"
	"strings"
)

// buildAnthropicUpstreamURL appends an Anthropic endpoint path without
// duplicating the /v1 segment when the configured base URL already includes it.
func buildAnthropicUpstreamURL(baseURL, path string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + path + "?beta=true"
	}

	trimmedBasePath := strings.TrimRight(parsedURL.Path, "/")
	if strings.HasSuffix(trimmedBasePath, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	parsedURL.Path = trimmedBasePath + path
	if parsedURL.RawPath != "" {
		rawBasePath := strings.TrimRight(parsedURL.RawPath, "/")
		rawEndpointPath := (&url.URL{Path: path}).EscapedPath()
		parsedURL.RawPath = rawBasePath + rawEndpointPath
	}

	query := parsedURL.Query()
	query.Set("beta", "true")
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}
