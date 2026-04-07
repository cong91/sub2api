package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	inviteBootstrapAPIStyleOpenAI    = "openai-responses"
	inviteBootstrapAPIStyleAnthropic = "anthropic-messages"

	inviteBootstrapProviderIDAntigravityClaude = "antigravity-claude"
	inviteBootstrapProviderIDAntigravityGemini = "antigravity-gemini"

	antigravityFlavorClaude = "claude"
	antigravityFlavorGemini = "gemini"
)

type InviteBootstrapContextBuilder interface {
	Supports(platform string) bool
	BuildProviders(ctx context.Context, group *Group) ([]InviteBootstrapProvider, error)
}

type InviteBootstrapContextFactory struct {
	builders []InviteBootstrapContextBuilder
}

func NewInviteBootstrapContextFactory(builders ...InviteBootstrapContextBuilder) *InviteBootstrapContextFactory {
	return &InviteBootstrapContextFactory{builders: builders}
}

func DefaultInviteBootstrapContextFactory(settingService *SettingService) *InviteBootstrapContextFactory {
	return NewInviteBootstrapContextFactory(
		&OpenAIBootstrapContextBuilder{settingService: settingService},
		&AnthropicBootstrapContextBuilder{settingService: settingService},
		&GeminiBootstrapContextBuilder{settingService: settingService},
		&AntigravityBootstrapContextBuilder{settingService: settingService},
	)
}

func (f *InviteBootstrapContextFactory) BuildProviders(ctx context.Context, group *Group) ([]InviteBootstrapProvider, error) {
	if group == nil {
		return nil, fmt.Errorf("group is required")
	}
	platform := normalizeInvitePlatform(group.Platform)
	for _, builder := range f.builders {
		if builder == nil || !builder.Supports(platform) {
			continue
		}
		return builder.BuildProviders(ctx, group)
	}
	return nil, fmt.Errorf("unsupported invite bootstrap platform: %s", group.Platform)
}

type OpenAIBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *OpenAIBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformOpenAI
}

func (b *OpenAIBootstrapContextBuilder) BuildProviders(ctx context.Context, _ *Group) ([]InviteBootstrapProvider, error) {
	models := make([]InviteBootstrapModel, 0, len(openai.DefaultModels))
	for idx, model := range openai.DefaultModels {
		models = append(models, InviteBootstrapModel{
			ID:          model.ID,
			Name:        model.DisplayName,
			Reasoning:   true,
			Recommended: idx == 0,
		})
	}
	if len(models) == 0 {
		models = []InviteBootstrapModel{{
			ID:          openai.DefaultTestModel,
			Name:        openai.DefaultTestModel,
			Reasoning:   true,
			Recommended: true,
		}}
	}

	return []InviteBootstrapProvider{{
		ProviderID:   PlatformOpenAI,
		ProviderName: "OpenAI",
		BaseURL:      inviteBootstrapProviderBaseURL(inviteBootstrapBaseURL(ctx, b.settingService), PlatformOpenAI),
		APIStyle:     inviteBootstrapAPIStyleOpenAI,
		Models:       models,
		DefaultModel: models[0].ID,
	}}, nil
}

type AnthropicBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *AnthropicBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformAnthropic
}

func (b *AnthropicBootstrapContextBuilder) BuildProviders(ctx context.Context, _ *Group) ([]InviteBootstrapProvider, error) {
	models := make([]InviteBootstrapModel, 0, len(claude.DefaultModels))
	for idx, model := range claude.DefaultModels {
		models = append(models, InviteBootstrapModel{
			ID:          model.ID,
			Name:        model.DisplayName,
			Reasoning:   true,
			Recommended: idx == 0,
		})
	}
	if len(models) == 0 {
		models = []InviteBootstrapModel{{
			ID:          claude.DefaultTestModel,
			Name:        claude.DefaultTestModel,
			Reasoning:   true,
			Recommended: true,
		}}
	}

	return []InviteBootstrapProvider{{
		ProviderID:   PlatformAnthropic,
		ProviderName: "Anthropic",
		BaseURL:      inviteBootstrapProviderBaseURL(inviteBootstrapBaseURL(ctx, b.settingService), PlatformAnthropic),
		APIStyle:     inviteBootstrapAPIStyleAnthropic,
		Models:       models,
		DefaultModel: models[0].ID,
	}}, nil
}

type GeminiBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *GeminiBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformGemini
}

func (b *GeminiBootstrapContextBuilder) BuildProviders(ctx context.Context, _ *Group) ([]InviteBootstrapProvider, error) {
	models := []InviteBootstrapModel{{
		ID:          "gemini-2.5-pro",
		Name:        "Gemini 2.5 Pro",
		Reasoning:   true,
		Recommended: true,
	}}
	return []InviteBootstrapProvider{{
		ProviderID:   PlatformGemini,
		ProviderName: "Gemini",
		BaseURL:      inviteBootstrapProviderBaseURL(inviteBootstrapBaseURL(ctx, b.settingService), PlatformGemini),
		APIStyle:     "google-native",
		Models:       models,
		DefaultModel: models[0].ID,
	}}, nil
}

type AntigravityBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *AntigravityBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformAntigravity
}

func (b *AntigravityBootstrapContextBuilder) BuildProviders(ctx context.Context, group *Group) ([]InviteBootstrapProvider, error) {
	sourceModels := antigravity.DefaultModels()
	if len(sourceModels) == 0 {
		return nil, fmt.Errorf("antigravity model catalog is empty")
	}

	claudeModels, geminiModels := splitAntigravityModels(sourceModels)
	fallbackModel := strings.TrimSpace("")
	if b.settingService != nil {
		fallbackModel = strings.TrimSpace(b.settingService.GetFallbackModel(ctx, PlatformAntigravity))
	}

	supportedFlavors := resolveAntigravitySupportedFlavors(group, fallbackModel)
	if len(supportedFlavors) == 0 {
		supportedFlavors = []string{antigravityFlavorClaude, antigravityFlavorGemini}
	}

	providers := make([]InviteBootstrapProvider, 0, 2)
	for _, flavor := range supportedFlavors {
		source := []antigravity.ClaudeModel{}
		providerID := ""
		providerName := ""
		apiStyle := inviteBootstrapAPIStyleAnthropic

		switch flavor {
		case antigravityFlavorClaude:
			source = claudeModels
			providerID = inviteBootstrapProviderIDAntigravityClaude
			providerName = "Antigravity Claude"
			apiStyle = inviteBootstrapAPIStyleAnthropic
		case antigravityFlavorGemini:
			source = geminiModels
			providerID = inviteBootstrapProviderIDAntigravityGemini
			providerName = "Antigravity Gemini"
			apiStyle = "google-native"
		default:
			continue
		}

		if len(source) == 0 {
			continue
		}

		defaultModel := resolveAntigravityDefaultModel(source, group, fallbackModel)
		models := buildInviteBootstrapModels(source, defaultModel)
		providers = append(providers, InviteBootstrapProvider{
			ProviderID:   providerID,
			ProviderName: providerName,
			BaseURL:      inviteBootstrapProviderBaseURL(inviteBootstrapBaseURL(ctx, b.settingService), providerID),
			APIStyle:     apiStyle,
			Models:       models,
			DefaultModel: defaultModel,
		})
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("antigravity bootstrap providers are empty")
	}

	return providers, nil
}

func splitAntigravityModels(source []antigravity.ClaudeModel) (claudeModels []antigravity.ClaudeModel, geminiModels []antigravity.ClaudeModel) {
	claudeModels = make([]antigravity.ClaudeModel, 0, len(source))
	geminiModels = make([]antigravity.ClaudeModel, 0, len(source))
	for _, model := range source {
		switch antigravityModelFlavor(model.ID) {
		case antigravityFlavorClaude:
			claudeModels = append(claudeModels, model)
		case antigravityFlavorGemini:
			geminiModels = append(geminiModels, model)
		}
	}
	return claudeModels, geminiModels
}

func antigravityModelFlavor(modelID string) string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.HasPrefix(modelID, "claude-"):
		return antigravityFlavorClaude
	case strings.HasPrefix(modelID, "gemini-"):
		return antigravityFlavorGemini
	default:
		return ""
	}
}

func resolveAntigravitySupportedFlavors(group *Group, fallbackModel string) []string {
	hasClaude, hasGemini := antigravityScopeFlags(group)
	if hasClaude && hasGemini {
		return []string{antigravityFlavorClaude, antigravityFlavorGemini}
	}
	if hasClaude {
		return []string{antigravityFlavorClaude}
	}
	if hasGemini {
		return []string{antigravityFlavorGemini}
	}
	if group != nil {
		if flavor := antigravityModelFlavor(group.DefaultMappedModel); flavor != "" {
			return []string{flavor}
		}
	}
	if flavor := antigravityModelFlavor(fallbackModel); flavor != "" {
		return []string{flavor}
	}
	return []string{antigravityFlavorClaude, antigravityFlavorGemini}
}

func antigravityScopeFlags(group *Group) (hasClaude bool, hasGemini bool) {
	if group == nil || len(group.SupportedModelScopes) == 0 {
		return false, false
	}
	for _, scope := range group.SupportedModelScopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		switch scope {
		case "claude":
			hasClaude = true
		case "gemini_text", "gemini_image":
			hasGemini = true
		}
	}
	return hasClaude, hasGemini
}

func resolveAntigravityFlavorFromScopes(group *Group) string {
	hasClaude, hasGemini := antigravityScopeFlags(group)
	if hasClaude && !hasGemini {
		return antigravityFlavorClaude
	}
	if hasGemini && !hasClaude {
		return antigravityFlavorGemini
	}
	return ""
}

func resolveAntigravityDefaultModel(sourceModels []antigravity.ClaudeModel, group *Group, fallbackModel string) string {
	if len(sourceModels) == 0 {
		return ""
	}
	candidateIDs := make([]string, 0, 2)
	if group != nil {
		candidateIDs = append(candidateIDs, strings.TrimSpace(group.DefaultMappedModel))
	}
	candidateIDs = append(candidateIDs, strings.TrimSpace(fallbackModel))

	for _, candidate := range candidateIDs {
		if candidate == "" {
			continue
		}
		for _, model := range sourceModels {
			if model.ID == candidate {
				return candidate
			}
		}
	}

	return sourceModels[0].ID
}

func buildInviteBootstrapModels(sourceModels []antigravity.ClaudeModel, defaultModel string) []InviteBootstrapModel {
	models := make([]InviteBootstrapModel, 0, len(sourceModels))
	for _, model := range sourceModels {
		models = append(models, InviteBootstrapModel{
			ID:          model.ID,
			Name:        model.DisplayName,
			Reasoning:   true,
			Recommended: model.ID == defaultModel,
		})
	}
	return models
}

func inviteBootstrapBaseURL(ctx context.Context, settingService *SettingService) string {
	if settingService == nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(settingService.GetAPIBaseURL(ctx)), "/")
}

func inviteBootstrapProviderBaseURL(baseURL, providerID string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return ""
	}
	switch providerID {
	case PlatformOpenAI, PlatformAnthropic:
		if strings.HasSuffix(trimmed, "/v1") {
			return trimmed
		}
		return trimmed + "/v1"
	case PlatformGemini:
		if strings.HasSuffix(trimmed, "/v1beta") {
			return trimmed
		}
		return trimmed + "/v1beta"
	case inviteBootstrapProviderIDAntigravityClaude:
		if strings.HasSuffix(trimmed, "/antigravity/v1") {
			return trimmed
		}
		return trimmed + "/antigravity/v1"
	case inviteBootstrapProviderIDAntigravityGemini:
		if strings.HasSuffix(trimmed, "/antigravity/v1beta") {
			return trimmed
		}
		return trimmed + "/antigravity/v1beta"
	default:
		return trimmed
	}
}

func normalizeInvitePlatform(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}
