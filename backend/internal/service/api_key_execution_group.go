package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// ExecutionGroupResolver resolves the request-scoped execution group from API key membership.
// group_ids[] remains canonical membership only; request routing must resolve the actual
// execution group based on protocol lane / request context.
func (k *APIKey) ExecutionGroupResolver(ctx context.Context) *Group {
	if k == nil {
		return nil
	}
	return k.executionGroupForContext(ctx)
}

// ExecutionGroupIDResolver returns the request-scoped execution group ID.
func (k *APIKey) ExecutionGroupIDResolver(ctx context.Context) *int64 {
	group := k.ExecutionGroupResolver(ctx)
	if group == nil {
		return nil
	}
	gid := group.ID
	return &gid
}

func (k *APIKey) executionGroupForContext(ctx context.Context) *Group {
	canonical := k.CanonicalGrantedGroups()
	if len(canonical) == 0 {
		return nil
	}

	lane := normalizeExecutionLane(executionLaneFromContext(ctx))
	if lane == "" {
		return canonical[0]
	}

	for _, granted := range canonical {
		if executionGroupMatchesLane(granted, lane) {
			return granted
		}
	}

	return nil
}

func executionLaneFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if platform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && strings.TrimSpace(platform) != "" {
		return platform
	}
	if lane, ok := ctx.Value(ctxkey.Platform).(string); ok && strings.TrimSpace(lane) != "" {
		return lane
	}
	return ""
}

func normalizeExecutionLane(lane string) string {
	lane = strings.ToLower(strings.TrimSpace(lane))
	switch lane {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity:
		return lane
	default:
		return ""
	}
}

func executionGroupMatchesLane(group *Group, lane string) bool {
	if !IsGroupContextValid(group) {
		return false
	}
	groupPlatform := strings.ToLower(strings.TrimSpace(group.Platform))
	switch lane {
	case PlatformOpenAI:
		return groupPlatform == PlatformOpenAI
	case PlatformAnthropic:
		return groupPlatform == PlatformAnthropic || groupPlatform == PlatformAntigravity
	case PlatformGemini:
		return groupPlatform == PlatformGemini || groupPlatform == PlatformAntigravity
	case PlatformAntigravity:
		return groupPlatform == PlatformAntigravity
	default:
		return false
	}
}
