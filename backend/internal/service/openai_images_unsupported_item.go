package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
)

const (
	OpenAIImagesUnsupportedItemReason  GatewayFailureReason = "openai_images_unsupported_item"
	OpenAIImagesUnsupportedItemMessage string               = "images contains an unsupported item"
)

// Only the top-level error object is authoritative; an echoed request or
// nested error must not turn an unrelated client or policy error into a retry.
func isOpenAIImagesUnsupportedItem(endpoint string, status int, body []byte) bool {
	if endpoint != openAIImagesEditsEndpoint || status != http.StatusBadRequest {
		return false
	}
	var response struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil || response.Error == nil {
		return false
	}
	return response.Error.Type == "invalid_request_error" && response.Error.Message == OpenAIImagesUnsupportedItemMessage
}

func newOpenAIImagesUnsupportedItemFailoverError(headers http.Header, body []byte) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:        http.StatusBadRequest,
		ResponseHeaders:   headers.Clone(),
		ResponseBody:      body,
		Scope:             GatewayFailureScopeProvider,
		Reason:            OpenAIImagesUnsupportedItemReason,
		NextAccountAction: NextAccountRetry,
	}
}

// OpenAIImagesUnsupportedItemFailoverAccountIDs excludes every currently
// schedulable image-route account that targets the same upstream provider.
func (s *OpenAIGatewayService) OpenAIImagesUnsupportedItemFailoverAccountIDs(
	ctx context.Context,
	groupID *int64,
	failed *Account,
	alreadyExcluded map[int64]struct{},
) (map[int64]struct{}, error) {
	provider, err := openAIImagesProviderIdentity(failed)
	if err != nil {
		return nil, err
	}
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil {
		return nil, fmt.Errorf("list image failover candidates: %w", err)
	}

	excluded := make(map[int64]struct{}, len(alreadyExcluded)+len(accounts)+1)
	for id := range alreadyExcluded {
		excluded[id] = struct{}{}
	}
	excluded[failed.ID] = struct{}{}
	for i := range accounts {
		candidate := &accounts[i]
		candidateProvider, identityErr := openAIImagesProviderIdentity(candidate)
		if identityErr != nil || candidateProvider == provider {
			// Unknown identity cannot be proven to be a different provider.
			excluded[candidate.ID] = struct{}{}
		}
	}
	return excluded, nil
}

func openAIImagesProviderIdentity(account *Account) (string, error) {
	if account == nil || account.Platform != PlatformOpenAI {
		return "", fmt.Errorf("image failover provider identity requires an OpenAI account")
	}
	parsed, err := url.Parse(strings.TrimSpace(account.GetOpenAIBaseURL()))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("image failover provider identity is unavailable")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("image failover provider identity is unavailable")
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	basePath := pathpkg.Clean("/" + strings.TrimSpace(parsed.Path))
	if basePath == "/" {
		basePath = ""
	}
	basePath = strings.TrimSuffix(basePath, "/v1")
	return host + basePath, nil
}
