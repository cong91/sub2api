package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type userGroupRateRepoHotpathStub struct {
	UserGroupRateRepository

	rate  *float64
	err   error
	wait  <-chan struct{}
	calls atomic.Int64
}

func (s *userGroupRateRepoHotpathStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.calls.Add(1)
	if s.wait != nil {
		<-s.wait
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}

type usageLogWindowBatchRepoStub struct {
	UsageLogRepository

	batchResult map[int64]*usagestats.AccountStats
	batchErr    error
	batchCalls  atomic.Int64

	singleResult map[int64]*usagestats.AccountStats
	singleErr    error
	singleCalls  atomic.Int64
}

func (s *usageLogWindowBatchRepoStub) GetAccountWindowStatsBatch(ctx context.Context, accountIDs []int64, startTime time.Time) (map[int64]*usagestats.AccountStats, error) {
	s.batchCalls.Add(1)
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[int64]*usagestats.AccountStats, len(accountIDs))
	for _, id := range accountIDs {
		if stats, ok := s.batchResult[id]; ok {
			out[id] = stats
		}
	}
	return out, nil
}

func (s *usageLogWindowBatchRepoStub) GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	s.singleCalls.Add(1)
	if s.singleErr != nil {
		return nil, s.singleErr
	}
	if stats, ok := s.singleResult[accountID]; ok {
		return stats, nil
	}
	return &usagestats.AccountStats{}, nil
}

type sessionLimitCacheHotpathStub struct {
	SessionLimitCache

	batchData map[int64]float64
	batchErr  error

	setData map[int64]float64
	setErr  error
}

func (s *sessionLimitCacheHotpathStub) GetWindowCostBatch(ctx context.Context, accountIDs []int64) (map[int64]float64, error) {
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[int64]float64, len(accountIDs))
	for _, id := range accountIDs {
		if v, ok := s.batchData[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func (s *sessionLimitCacheHotpathStub) SetWindowCost(ctx context.Context, accountID int64, cost float64) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.setData == nil {
		s.setData = make(map[int64]float64)
	}
	s.setData[accountID] = cost
	return nil
}

type modelsListAccountRepoStub struct {
	AccountRepository

	byGroup map[int64][]Account
	all     []Account
	err     error

	listByGroupCalls atomic.Int64
	listAllCalls     atomic.Int64
}

type stickyGatewayCacheHotpathStub struct {
	GatewayCache

	stickyID int64
	getCalls atomic.Int64
}

func (s *stickyGatewayCacheHotpathStub) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	s.getCalls.Add(1)
	if s.stickyID > 0 {
		return s.stickyID, nil
	}
	return 0, errors.New("not found")
}

func (s *stickyGatewayCacheHotpathStub) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) SetGrokVideoPendingBilling(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return nil
}
func (s *stickyGatewayCacheHotpathStub) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return nil, nil
}
func (s *stickyGatewayCacheHotpathStub) ClaimGrokVideoBilled(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (s *stickyGatewayCacheHotpathStub) ReleaseGrokVideoBilled(_ context.Context, _ string) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) SetReasoningContent(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}
func (s *stickyGatewayCacheHotpathStub) GetReasoningContent(_ context.Context, _ string) (string, error) {
	return "", ErrReasoningContentNotFound
}

func (s *modelsListAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	s.listByGroupCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	accounts, ok := s.byGroup[groupID]
	if !ok {
		return nil, nil
	}
	out := make([]Account, len(accounts))
	copy(out, accounts)
	return out, nil
}

func (s *modelsListAccountRepoStub) ListSchedulable(ctx context.Context) ([]Account, error) {
	s.listAllCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]Account, len(s.all))
	copy(out, s.all)
	return out, nil
}

func resetGatewayHotpathStatsForTest() {
	windowCostPrefetchCacheHitTotal.Store(0)
	windowCostPrefetchCacheMissTotal.Store(0)
	windowCostPrefetchBatchSQLTotal.Store(0)
	windowCostPrefetchFallbackTotal.Store(0)
	windowCostPrefetchErrorTotal.Store(0)

	userGroupRateCacheHitTotal.Store(0)
	userGroupRateCacheMissTotal.Store(0)
	userGroupRateCacheLoadTotal.Store(0)
	userGroupRateCacheSFSharedTotal.Store(0)
	userGroupRateCacheFallbackTotal.Store(0)

	modelsListCacheHitTotal.Store(0)
	modelsListCacheMissTotal.Store(0)
	modelsListCacheStoreTotal.Store(0)
}

func TestGetUserGroupRateMultiplier_UsesCacheAndSingleflight(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	rate := 1.7
	unblock := make(chan struct{})
	repo := &userGroupRateRepoHotpathStub{
		rate: &rate,
		wait: unblock,
	}
	svc := &GatewayService{
		userGroupRateRepo:  repo,
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				UserGroupRateCacheTTLSeconds: 30,
			},
		},
	}

	const concurrent = 12
	results := make([]float64, concurrent)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.2)
		}(i)
	}

	close(start)
	// Wait for every caller to have recorded its cache miss before releasing the
	// loader. A fixed sleep raced here: a goroutine that reached the cache after
	// the singleflight load had already finished got a hit instead of a miss, and
	// the miss assertion below saw 11 of 12. The miss counter is the observable
	// that says "all callers are now inside the singleflight group".
	require.Eventually(t, func() bool {
		_, miss, _, _, _ := GatewayUserGroupRateCacheStats()
		return miss == int64(concurrent)
	}, 5*time.Second, time.Millisecond, "all callers must miss the cache before the loader is released")
	close(unblock)
	wg.Wait()

	for _, got := range results {
		require.Equal(t, rate, got)
	}
	require.Equal(t, int64(1), repo.calls.Load())

	// 再次读取应命中缓存，不再回源。
	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.2)
	require.Equal(t, rate, got)
	require.Equal(t, int64(1), repo.calls.Load())

	hit, miss, load, sfShared, fallback := GatewayUserGroupRateCacheStats()
	require.GreaterOrEqual(t, hit, int64(1))
	require.Equal(t, int64(12), miss)
	require.Equal(t, int64(1), load)
	require.GreaterOrEqual(t, sfShared, int64(1))
	require.Equal(t, int64(0), fallback)
}

func TestGetUserGroupRateMultiplier_FallbackOnRepoError(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("db down"),
	}
	svc := &GatewayService{
		userGroupRateRepo:  repo,
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				UserGroupRateCacheTTLSeconds: 30,
			},
		},
	}

	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.25)
	require.Equal(t, 1.25, got)
	require.Equal(t, int64(1), repo.calls.Load())

	_, _, _, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(1), fallback)
}

func TestGetUserGroupRateMultiplier_CacheHitAndNilRepo(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("should not be called"),
	}
	svc := &GatewayService{
		userGroupRateRepo:  repo,
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
	}
	key := "101:202"
	svc.userGroupRateCache.Set(key, 2.3, time.Minute)

	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.1)
	require.Equal(t, 2.3, got)

	hit, miss, load, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(0), miss)
	require.Equal(t, int64(0), load)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), repo.calls.Load())

	// 无 repo 时直接返回分组默认倍率
	svc2 := &GatewayService{
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
	}
	svc2.userGroupRateCache.Set(key, 1.9, time.Minute)
	require.Equal(t, 1.9, svc2.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.4))
	require.Equal(t, 1.4, svc2.getUserGroupRateMultiplier(context.Background(), 0, 202, 1.4))
	svc2.userGroupRateCache.Delete(key)
	require.Equal(t, 1.4, svc2.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.4))
}

func TestWithWindowCostPrefetch_BatchReadAndContextReuse(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	accounts := []Account{
		{
			ID:                 1,
			Platform:           PlatformAnthropic,
			Type:               AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
		{
			ID:                 2,
			Platform:           PlatformAnthropic,
			Type:               AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
		{
			ID:       3,
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Extra:    map[string]any{"window_cost_limit": 100.0},
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchData: map[int64]float64{
			1: 11.0,
		},
	}
	repo := &usageLogWindowBatchRepoStub{
		batchResult: map[int64]*usagestats.AccountStats{
			2: {StandardCost: 22.0},
		},
	}

	svc := &GatewayService{
		sessionLimitCache: cache,
		usageLogRepo:      repo,
	}

	ctx := svc.withWindowCostPrefetch(context.Background(), accounts)

	cost1, ok1 := windowCostFromPrefetchContext(ctx, 1)
	require.True(t, ok1)
	require.Equal(t, 11.0, cost1)

	cost2, ok2 := windowCostFromPrefetchContext(ctx, 2)
	require.True(t, ok2)
	require.Equal(t, 22.0, cost2)

	_, ok3 := windowCostFromPrefetchContext(ctx, 3)
	require.False(t, ok3)

	require.Equal(t, int64(1), repo.batchCalls.Load())

	hit, miss, batch, fallback, err2 := GatewayWindowCostPrefetchStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(1), miss)
	require.Equal(t, int64(1), batch)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), err2)
}

func TestWithWindowCostPrefetch_FallbackWhenBothDown(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	accounts := []Account{
		{
			ID:                 1,
			Platform:           PlatformAnthropic,
			Type:               AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchErr: errors.New("cache down"),
	}
	repo := &usageLogWindowBatchRepoStub{
		batchErr: errors.New("db down"),
	}

	svc := &GatewayService{
		sessionLimitCache: cache,
		usageLogRepo:      repo,
	}

	ctx := svc.withWindowCostPrefetch(context.Background(), accounts)

	_, ok := windowCostFromPrefetchContext(ctx, 1)
	require.False(t, ok)

	_, _, _, fallback, errCount := GatewayWindowCostPrefetchStats()
	require.Equal(t, int64(1), fallback)
	require.Equal(t, int64(1), errCount)
}

func TestGetAvailableModels_CacheAndDedupe(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	groupID := int64(7)
	repo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{
			groupID: {
				{
					ID:       1,
					Platform: PlatformAnthropic,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"claude-3-5-sonnet": "claude-3-5-sonnet-20241022",
							"claude-3-opus":     "claude-3-opus-20240229",
						},
					},
				},
				{
					ID:       2,
					Platform: PlatformGemini,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-2.5-pro": "gemini-2.5-pro-exp-0827",
						},
					},
				},
				{
					ID:       3,
					Platform: PlatformAnthropic,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"claude-3-opus": "claude-3-opus-20240229",
						},
					},
				},
			},
		},
	}

	svc := &GatewayService{
		accountRepo:        repo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}

	models1 := svc.GetAvailableModels(context.Background(), &groupID, PlatformAnthropic)
	require.ElementsMatch(t, []string{"claude-3-5-sonnet", "claude-3-opus"}, models1)
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())

	models2 := svc.GetAvailableModels(context.Background(), &groupID, PlatformAnthropic)
	require.ElementsMatch(t, []string{"claude-3-5-sonnet", "claude-3-opus"}, models2)
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())

	models3 := svc.GetAvailableModels(context.Background(), &groupID, PlatformGemini)
	require.Equal(t, []string{"gemini-2.5-pro"}, models3)
	require.Equal(t, int64(2), repo.listByGroupCalls.Load())

	hit, miss, store := GatewayModelsListCacheStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(2), miss)
	require.Equal(t, int64(2), store)
}

func TestGetAvailableModels_GlobalListFallsBackToAll(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	groupID := int64(8)
	errRepo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{
			groupID: {
				{
					ID:       1,
					Platform: PlatformAnthropic,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"claude-3-5-sonnet": "claude-3-5-sonnet",
						},
					},
				},
			},
		},
		err: errors.New("repo fail"),
	}
	svcErr := &GatewayService{
		accountRepo:        errRepo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}
	modelsErr := svcErr.GetAvailableModels(context.Background(), nil, "")
	require.Empty(t, modelsErr)

	okRepo := &modelsListAccountRepoStub{
		all: []Account{
			{
				ID:       1,
				Platform: PlatformAnthropic,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-3-5-sonnet": "claude-3-5-sonnet",
					},
				},
			},
			{
				ID:       2,
				Platform: PlatformGemini,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"gemini-2.5-pro": "gemini-2.5-pro",
					},
				},
			},
		},
	}
	svcOK := &GatewayService{
		accountRepo:        okRepo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}
	models := svcOK.GetAvailableModels(context.Background(), nil, "")
	require.Equal(t, []string{"claude-3-5-sonnet", "gemini-2.5-pro"}, models)
	require.Equal(t, int64(1), okRepo.listAllCalls.Load())
}

func TestGetAvailableModels_ErrorAndGlobalListBranches(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	groupID := int64(9)
	errRepo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{groupID: {{ID: 1, Platform: PlatformAnthropic}}},
		err:     errors.New("repo fail"),
	}
	svcErr := &GatewayService{
		accountRepo:        errRepo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}
	modelsErr := svcErr.GetAvailableModels(context.Background(), &groupID, PlatformAnthropic)
	require.Empty(t, modelsErr)

	okRepo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{
			groupID: {
				{
					ID:       1,
					Platform: PlatformAnthropic,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"claude-3-5-sonnet": "claude-3-5-sonnet",
						},
					},
				},
				{
					ID:       2,
					Platform: PlatformGemini,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-2.5-pro": "gemini-2.5-pro",
						},
					},
				},
			},
		},
	}
	svcOK := &GatewayService{
		accountRepo:        okRepo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}
	models := svcOK.GetAvailableModels(context.Background(), &groupID, "")
	require.Equal(t, []string{"claude-3-5-sonnet", "gemini-2.5-pro"}, models)

	modelsFilteredEmpty := svcOK.GetAvailableModels(context.Background(), &groupID, "unknown")
	require.Empty(t, modelsFilteredEmpty)

	require.Equal(t, int64(2), okRepo.listByGroupCalls.Load())
	require.Equal(t, int64(0), okRepo.listAllCalls.Load())
}

func TestGetAvailableModels_OpenAIPassthroughUsesDefaultFallback(t *testing.T) {
	groupID := int64(10)

	tests := []struct {
		name     string
		accounts []Account
		want     []string
	}{
		{
			name: "passthrough only ignores stale mapping",
			accounts: []Account{
				{
					ID:          1,
					Platform:    PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"stale-model": "upstream-model"}},
					Extra:       map[string]any{"openai_passthrough": true},
				},
			},
			want: nil,
		},
		{
			name: "passthrough wins over ordinary account mapping",
			accounts: []Account{
				{
					ID:          2,
					Platform:    PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-upstream"}},
				},
				{
					ID:          3,
					Platform:    PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"stale-model": "upstream-model"}},
					Extra:       map[string]any{"openai_passthrough": true},
				},
			},
			want: nil,
		},
		{
			name: "ordinary accounts preserve mapped whitelist",
			accounts: []Account{
				{
					ID:          4,
					Platform:    PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-upstream"}},
				},
			},
			want: []string{"configured-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: tt.accounts}}
			svc := &GatewayService{
				accountRepo:        repo,
				modelsListCache:    gocache.New(time.Minute, time.Minute),
				modelsListCacheTTL: time.Minute,
			}

			require.Equal(t, tt.want, svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenAI))
		})
	}
}

func TestGetAvailableModels_GlobalListPreservesMappedModelsWithOpenAIPassthrough(t *testing.T) {
	groupID := int64(11)
	repo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{
			groupID: {
				{
					ID:       1,
					Platform: PlatformOpenAI,
					Extra:    map[string]any{"openai_passthrough": true},
				},
				{
					ID:          2,
					Platform:    PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-mapped": "claude-upstream"}},
				},
			},
		},
	}
	svc := &GatewayService{
		accountRepo:        repo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}

	require.Equal(t, []string{"claude-mapped"}, svc.GetAvailableModels(context.Background(), &groupID, ""))
}

func TestGetAvailableModels_OpenCodeUsesRawDefaultModelMapping(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	groupID := int64(28)
	repo := &modelsListAccountRepoStub{
		byGroup: map[int64][]Account{
			groupID: {
				{ID: 1, Platform: PlatformOpenCode, Type: AccountTypeAPIKey, Credentials: map[string]any{}},
			},
		},
	}
	svc := &GatewayService{
		accountRepo:        repo,
		modelsListCache:    gocache.New(time.Minute, time.Minute),
		modelsListCacheTTL: time.Minute,
	}

	models := svc.GetAvailableModels(context.Background(), &groupID, PlatformOpenCode)
	require.Contains(t, models, "glm-5.2")
	require.Contains(t, models, "deepseek-v4-pro")
	require.Contains(t, models, "minimax-m3")
	require.NotContains(t, models, "claude-sonnet-4-6")
	for _, model := range models {
		require.NotContains(t, model, "opencode/")
	}
}

func TestGatewayHotpathHelpers_CacheTTLAndStickyContext(t *testing.T) {
	t.Run("resolve_user_group_rate_cache_ttl", func(t *testing.T) {
		require.Equal(t, defaultUserGroupRateCacheTTL, resolveUserGroupRateCacheTTL(nil))

		cfg := &config.Config{
			Gateway: config.GatewayConfig{
				UserGroupRateCacheTTLSeconds: 45,
			},
		}
		require.Equal(t, 45*time.Second, resolveUserGroupRateCacheTTL(cfg))
	})

	t.Run("resolve_models_list_cache_ttl", func(t *testing.T) {
		require.Equal(t, defaultModelsListCacheTTL, resolveModelsListCacheTTL(nil))

		cfg := &config.Config{
			Gateway: config.GatewayConfig{
				ModelsListCacheTTLSeconds: 20,
			},
		}
		require.Equal(t, 20*time.Second, resolveModelsListCacheTTL(cfg))
	})

	t.Run("prefetched_sticky_account_id_from_context", func(t *testing.T) {
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(context.TODO(), nil))
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(context.Background(), nil))

		ctx := context.WithValue(context.Background(), ctxkey.PrefetchedStickyAccountID, int64(123))
		ctx = context.WithValue(ctx, ctxkey.PrefetchedStickyGroupID, int64(0))
		require.Equal(t, int64(123), prefetchedStickyAccountIDFromContext(ctx, nil))

		groupID := int64(9)
		ctx2 := context.WithValue(context.Background(), ctxkey.PrefetchedStickyAccountID, 456)
		ctx2 = context.WithValue(ctx2, ctxkey.PrefetchedStickyGroupID, groupID)
		require.Equal(t, int64(456), prefetchedStickyAccountIDFromContext(ctx2, &groupID))

		ctx3 := context.WithValue(context.Background(), ctxkey.PrefetchedStickyAccountID, "invalid")
		ctx3 = context.WithValue(ctx3, ctxkey.PrefetchedStickyGroupID, groupID)
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(ctx3, &groupID))

		ctx4 := context.WithValue(context.Background(), ctxkey.PrefetchedStickyAccountID, int64(789))
		ctx4 = context.WithValue(ctx4, ctxkey.PrefetchedStickyGroupID, int64(10))
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(ctx4, &groupID))
	})

	t.Run("window_cost_from_prefetch_context", func(t *testing.T) {
		require.Equal(t, false, func() bool {
			_, ok := windowCostFromPrefetchContext(context.TODO(), 0)
			return ok
		}())
		require.Equal(t, false, func() bool {
			_, ok := windowCostFromPrefetchContext(context.Background(), 1)
			return ok
		}())

		ctx := context.WithValue(context.Background(), windowCostPrefetchContextKey, map[int64]float64{
			9: 12.34,
		})
		cost, ok := windowCostFromPrefetchContext(ctx, 9)
		require.True(t, ok)
		require.Equal(t, 12.34, cost)
	})
}

func TestInvalidateAvailableModelsCache_ByDimensions(t *testing.T) {
	svc := &GatewayService{
		modelsListCache: gocache.New(time.Minute, time.Minute),
	}
	group9 := int64(9)
	group10 := int64(10)
	svc.modelsListCache.Set(modelsListCacheKey(&group9, PlatformAnthropic), []string{"a"}, time.Minute)
	svc.modelsListCache.Set(modelsListCacheKey(&group9, PlatformGemini), []string{"b"}, time.Minute)
	svc.modelsListCache.Set(modelsListCacheKey(&group10, PlatformAnthropic), []string{"c"}, time.Minute)
	svc.modelsListCache.Set("invalid-key", []string{"d"}, time.Minute)

	t.Run("invalidate_group_and_platform", func(t *testing.T) {
		svc.InvalidateAvailableModelsCache(&group9, PlatformAnthropic)
		_, found := svc.modelsListCache.Get(modelsListCacheKey(&group9, PlatformAnthropic))
		require.False(t, found)
		_, stillFound := svc.modelsListCache.Get(modelsListCacheKey(&group9, PlatformGemini))
		require.True(t, stillFound)
	})

	t.Run("invalidate_group_only", func(t *testing.T) {
		svc.InvalidateAvailableModelsCache(&group9, "")
		_, foundA := svc.modelsListCache.Get(modelsListCacheKey(&group9, PlatformAnthropic))
		_, foundB := svc.modelsListCache.Get(modelsListCacheKey(&group9, PlatformGemini))
		require.False(t, foundA)
		require.False(t, foundB)
		_, foundOtherGroup := svc.modelsListCache.Get(modelsListCacheKey(&group10, PlatformAnthropic))
		require.True(t, foundOtherGroup)
	})

	t.Run("invalidate_platform_only", func(t *testing.T) {
		// 重建数据后仅按 platform 失效
		svc.modelsListCache.Set(modelsListCacheKey(&group9, PlatformAnthropic), []string{"a"}, time.Minute)
		svc.modelsListCache.Set(modelsListCacheKey(&group9, PlatformGemini), []string{"b"}, time.Minute)
		svc.modelsListCache.Set(modelsListCacheKey(&group10, PlatformAnthropic), []string{"c"}, time.Minute)

		svc.InvalidateAvailableModelsCache(nil, PlatformAnthropic)
		_, found9Anthropic := svc.modelsListCache.Get(modelsListCacheKey(&group9, PlatformAnthropic))
		_, found10Anthropic := svc.modelsListCache.Get(modelsListCacheKey(&group10, PlatformAnthropic))
		_, found9Gemini := svc.modelsListCache.Get(modelsListCacheKey(&group9, PlatformGemini))
		require.False(t, found9Anthropic)
		require.False(t, found10Anthropic)
		require.True(t, found9Gemini)
	})
}
