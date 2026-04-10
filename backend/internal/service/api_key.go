package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
)

// API Key status constants
const (
	StatusAPIKeyActive         = "active"
	StatusAPIKeyDisabled       = "disabled"
	StatusAPIKeyQuotaExhausted = "quota_exhausted"
	StatusAPIKeyExpired        = "expired"
)

// Rate limit window durations
const (
	RateLimitWindow5h = 5 * time.Hour
	RateLimitWindow1d = 24 * time.Hour
	RateLimitWindow7d = 7 * 24 * time.Hour
)

// IsWindowExpired returns true if the window starting at windowStart has exceeded the given duration.
// A nil windowStart is treated as expired — no initialized window means any accumulated usage is stale.
func IsWindowExpired(windowStart *time.Time, duration time.Duration) bool {
	return windowStart == nil || time.Since(*windowStart) >= duration
}

type APIKey struct {
	ID          int64
	UserID      int64
	Key         string
	Name        string
	GroupIDs    []int64
	Groups      []*Group
	Status      string
	IPWhitelist []string
	IPBlacklist []string
	// 预编译的 IP 规则，用于认证热路径避免重复 ParseIP/ParseCIDR。
	CompiledIPWhitelist *ip.CompiledIPRules `json:"-"`
	CompiledIPBlacklist *ip.CompiledIPRules `json:"-"`
	LastUsedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	User                *User

	// Quota fields
	Quota     float64    // Quota limit in USD (0 = unlimited)
	QuotaUsed float64    // Used quota amount
	ExpiresAt *time.Time // Expiration time (nil = never expires)

	// Rate limit fields
	RateLimit5h   float64    // Rate limit in USD per 5h (0 = unlimited)
	RateLimit1d   float64    // Rate limit in USD per 1d (0 = unlimited)
	RateLimit7d   float64    // Rate limit in USD per 7d (0 = unlimited)
	Usage5h       float64    // Used amount in current 5h window
	Usage1d       float64    // Used amount in current 1d window
	Usage7d       float64    // Used amount in current 7d window
	Window5hStart *time.Time // Start of current 5h window
	Window1dStart *time.Time // Start of current 1d window
	Window7dStart *time.Time // Start of current 7d window
}

// EffectiveGroup returns the canonical request-time execution group.
// Membership remains represented by group_ids[]/groups[]. The effective scalar
// is derived from that ordered membership (first canonical usable group), not
// stored as a separate membership field.
// During the strict group_ids[] cutover, some in-flight callers/tests still
// hydrate Groups without full runtime-valid metadata; for billing/routing
// semantics we still need the first ordered hydrated group to be the effective
// group instead of silently collapsing to nil.
func (k *APIKey) EffectiveGroup() *Group {
	if k == nil {
		return nil
	}
	if len(k.Groups) > 0 {
		ordered := k.CanonicalGrantedGroups()
		if len(ordered) > 0 {
			return ordered[0]
		}
		for _, granted := range k.Groups {
			if granted != nil {
				return granted
			}
		}
	}
	return nil
}

// EffectiveGroupID returns the canonical request-time execution group scalar
// derived from group_ids[] ordering / hydrated groups[]. It is not a separate
// membership contract.
func (k *APIKey) EffectiveGroupID() *int64 {
	if k == nil {
		return nil
	}
	if effective := k.EffectiveGroup(); effective != nil {
		gid := effective.ID
		return &gid
	}
	if len(k.GroupIDs) > 0 {
		gid := k.GroupIDs[0]
		return &gid
	}
	return nil
}

// CanonicalGrantedGroups returns runtime-usable groups[] (hydrated, valid, deduplicated, ordered by group_ids first).
func (k *APIKey) CanonicalGrantedGroups() []*Group {
	if k == nil || len(k.Groups) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(k.Groups))
	byID := make(map[int64]*Group, len(k.Groups))
	for _, granted := range k.Groups {
		if !IsGroupContextValid(granted) {
			continue
		}
		if _, ok := seen[granted.ID]; ok {
			continue
		}
		seen[granted.ID] = struct{}{}
		byID[granted.ID] = granted
	}
	if len(byID) == 0 {
		return nil
	}
	out := make([]*Group, 0, len(byID))
	for _, gid := range k.GroupIDs {
		if g, ok := byID[gid]; ok {
			out = append(out, g)
			delete(byID, gid)
		}
	}
	for _, granted := range k.Groups {
		if granted == nil {
			continue
		}
		if _, ok := byID[granted.ID]; !ok {
			continue
		}
		out = append(out, granted)
		delete(byID, granted.ID)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GroupForPlatform returns the effective execution group for a requested
// platform when one exists in membership groups[], otherwise it falls back to
// the canonical effective group.
func (k *APIKey) GroupForPlatform(platform string) *Group {
	if k == nil {
		return nil
	}
	canonical := k.CanonicalGrantedGroups()
	if len(canonical) == 0 {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return canonical[0]
	}
	for _, granted := range canonical {
		if strings.EqualFold(strings.TrimSpace(granted.Platform), platform) {
			return granted
		}
	}
	return canonical[0]
}

func (k *APIKey) IsActive() bool {
	return k.Status == StatusActive
}

// HasRateLimits returns true if any rate limit window is configured
func (k *APIKey) HasRateLimits() bool {
	return k.RateLimit5h > 0 || k.RateLimit1d > 0 || k.RateLimit7d > 0
}

// IsExpired checks if the API key has expired
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// IsQuotaExhausted checks if the API key quota is exhausted
func (k *APIKey) IsQuotaExhausted() bool {
	if k.Quota <= 0 {
		return false // unlimited
	}
	return k.QuotaUsed >= k.Quota
}

// GetQuotaRemaining returns remaining quota (-1 for unlimited)
func (k *APIKey) GetQuotaRemaining() float64 {
	if k.Quota <= 0 {
		return -1 // unlimited
	}
	remaining := k.Quota - k.QuotaUsed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetDaysUntilExpiry returns days until expiry (-1 for never expires)
func (k *APIKey) GetDaysUntilExpiry() int {
	if k.ExpiresAt == nil {
		return -1 // never expires
	}
	duration := time.Until(*k.ExpiresAt)
	if duration < 0 {
		return 0
	}
	return int(duration.Hours() / 24)
}

// EffectiveUsage5h returns the 5h window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage5h() float64 {
	if IsWindowExpired(k.Window5hStart, RateLimitWindow5h) {
		return 0
	}
	return k.Usage5h
}

// EffectiveUsage1d returns the 1d window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage1d() float64 {
	if IsWindowExpired(k.Window1dStart, RateLimitWindow1d) {
		return 0
	}
	return k.Usage1d
}

// EffectiveUsage7d returns the 7d window usage, or 0 if the window has expired.
func (k *APIKey) EffectiveUsage7d() float64 {
	if IsWindowExpired(k.Window7dStart, RateLimitWindow7d) {
		return 0
	}
	return k.Usage7d
}

// APIKeyListFilters holds optional filtering parameters for listing API keys.
type APIKeyListFilters struct {
	Search   string
	Status   string
	GroupIDs []int64 // empty=不筛选; values filter keys containing any requested group id
}
