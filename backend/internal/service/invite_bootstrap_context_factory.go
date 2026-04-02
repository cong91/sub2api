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
	Build(ctx context.Context, group *Group) (InviteBootstrapContext, error)
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
		&AntigravityBootstrapContextBuilder{settingService: settingService},
	)
}

func (f *InviteBootstrapContextFactory) Build(ctx context.Context, group *Group) (InviteBootstrapContext, error) {
	if group == nil {
		return InviteBootstrapContext{}, fmt.Errorf("group is required")
	}
	platform := normalizeInvitePlatform(group.Platform)
	for _, builder := range f.builders {
		if builder == nil || !builder.Supports(platform) {
			continue
		}
		return builder.Build(ctx, group)
	}
	return InviteBootstrapContext{}, fmt.Errorf("unsupported invite bootstrap platform: %s", group.Platform)
}

type OpenAIBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *OpenAIBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformOpenAI
}

func (b *OpenAIBootstrapContextBuilder) Build(ctx context.Context, _ *Group) (InviteBootstrapContext, error) {
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

	return InviteBootstrapContext{
		ProviderID:   PlatformOpenAI,
		ProviderName: "OpenAI",
		BaseURL:      inviteBootstrapBaseURL(ctx, b.settingService),
		APIStyle:     inviteBootstrapAPIStyleOpenAI,
		Models:       models,
		DefaultModel: models[0].ID,
	}, nil
}

type AnthropicBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *AnthropicBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformAnthropic
}

func (b *AnthropicBootstrapContextBuilder) Build(ctx context.Context, _ *Group) (InviteBootstrapContext, error) {
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

	return InviteBootstrapContext{
		ProviderID:   PlatformAnthropic,
		ProviderName: "Anthropic",
		BaseURL:      inviteBootstrapBaseURL(ctx, b.settingService),
		APIStyle:     inviteBootstrapAPIStyleAnthropic,
		Models:       models,
		DefaultModel: models[0].ID,
	}, nil
}

type AntigravityBootstrapContextBuilder struct {
	settingService *SettingService
}

func (b *AntigravityBootstrapContextBuilder) Supports(platform string) bool {
	return normalizeInvitePlatform(platform) == PlatformAntigravity
}

func (b *AntigravityBootstrapContextBuilder) Build(ctx context.Context, group *Group) (InviteBootstrapContext, error) {
	sourceModels := antigravity.DefaultModels()
	if len(sourceModels) == 0 {
		return InviteBootstrapContext{}, fmt.Errorf("antigravity model catalog is empty")
	}

	claudeModels, geminiModels := splitAntigravityModels(sourceModels)
	fallbackModel := strings.TrimSpace("")
	if b.settingService != nil {
		fallbackModel = strings.TrimSpace(b.settingService.GetFallbackModel(ctx, PlatformAntigravity))
	}

	flavor := resolveAntigravityFlavor(group, fallbackModel)

	selectedSourceModels := sourceModels
	switch flavor {
	case antigravityFlavorClaude:
		if len(claudeModels) > 0 {
			selectedSourceModels = claudeModels
		}
	case antigravityFlavorGemini:
		if len(geminiModels) > 0 {
			selectedSourceModels = geminiModels
		}
	}

	defaultModel := resolveAntigravityDefaultModel(selectedSourceModels, group, fallbackModel)
	models := buildInviteBootstrapModels(selectedSourceModels, defaultModel)

	providerID := PlatformAntigravity
	providerName := "Antigravity"
	apiStyle := inviteBootstrapAPIStyleAnthropic
	if flavor == antigravityFlavorClaude {
		providerID = inviteBootstrapProviderIDAntigravityClaude
		providerName = "Antigravity Claude"
		apiStyle = inviteBootstrapAPIStyleAnthropic
	} else if flavor == antigravityFlavorGemini {
		providerID = inviteBootstrapProviderIDAntigravityGemini
		providerName = "Antigravity Gemini"
		apiStyle = inviteBootstrapAPIStyleOpenAI
	}

	return InviteBootstrapContext{
		ProviderID:   providerID,
		ProviderName: providerName,
		BaseURL:      inviteBootstrapBaseURL(ctx, b.settingService),
		APIStyle:     apiStyle,
		Models:       models,
		DefaultModel: defaultModel,
	}, nil
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

func resolveAntigravityFlavor(group *Group, fallbackModel string) string {
	if flavor := resolveAntigravityFlavorFromScopes(group); flavor != "" {
		return flavor
	}
	if group != nil {
		if flavor := antigravityModelFlavor(group.DefaultMappedModel); flavor != "" {
			return flavor
		}
	}
	if flavor := antigravityModelFlavor(fallbackModel); flavor != "" {
		return flavor
	}
	return ""
}

func resolveAntigravityFlavorFromScopes(group *Group) string {
	if group == nil || len(group.SupportedModelScopes) == 0 {
		return ""
	}
	hasClaude := false
	hasGemini := false
	for _, scope := range group.SupportedModelScopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		switch scope {
		case "claude":
			hasClaude = true
		case "gemini_text", "gemini_image":
			hasGemini = true
		}
	}
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
	return strings.TrimSpace(settingService.GetAPIBaseURL(ctx))
}

func normalizeInvitePlatform(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}
