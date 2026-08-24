package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func TestNormalizeUserModelBlock(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		model    string
		want     UserModelBlock
		wantErr  bool
	}{
		{
			name:     "trims exact identifiers",
			platform: " openai ",
			model:    " gpt-4.1 ",
			want:     UserModelBlock{Platform: "openai", Model: "gpt-4.1"},
		},
		{name: "rejects empty platform", platform: " ", model: "gpt-4.1", wantErr: true},
		{name: "rejects empty model", platform: "openai", model: " ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeUserModelBlock(tt.platform, tt.model)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeUserModelBlock() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("NormalizeUserModelBlock() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSelectAccountWithLoadAwarenessChecksUserModelBlockBeforeScheduling(t *testing.T) {
	repo := &userModelBlockTestRepo{blocked: true}
	svc := &GatewayService{
		userModelBlockRepo: repo,
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, "openai")

	_, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "gpt-4.1", nil, "", 42)
	if !errors.Is(err, ErrUserModelBlocked) {
		t.Fatalf("SelectAccountWithLoadAwareness() error = %v, want ErrUserModelBlocked", err)
	}
	if repo.lastUserID != 42 || repo.lastBlock != (UserModelBlock{Platform: "openai", Model: "gpt-4.1"}) {
		t.Fatalf("load-aware check used %+v/%d, want exact key and user", repo.lastBlock, repo.lastUserID)
	}
}

func TestSelectAccountWithLoadAwarenessChecksUserModelBlockBeforeStickyLookup(t *testing.T) {
	repo := &userModelBlockTestRepo{blocked: true}
	cache := &userModelBlockCacheProbe{}
	svc := &GatewayService{
		userModelBlockRepo: repo,
		cache:              cache,
	}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, PlatformOpenAI)

	_, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sticky-session", "gpt-4.1", nil, "", 42)
	if !errors.Is(err, ErrUserModelBlocked) {
		t.Fatalf("SelectAccountWithLoadAwareness() error = %v, want ErrUserModelBlocked", err)
	}
	if cache.getCalls != 0 {
		t.Fatalf("sticky cache reads = %d, want 0 before model block rejection", cache.getCalls)
	}
}

func TestSelectAccountWithLoadAwarenessUsesResolvedCompositeOfferIdentity(t *testing.T) {
	repo := &userModelBlockTestRepo{blocked: true}
	svc := &GatewayService{
		userModelBlockRepo: repo,
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))
	ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{
		Matched:        true,
		PublicModel:    "public-alias",
		TargetPlatform: PlatformOpenAI,
		UpstreamModel:  "gpt-4.1",
	})

	_, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "public-alias", nil, "", 42)
	if !errors.Is(err, ErrUserModelBlocked) {
		t.Fatalf("SelectAccountWithLoadAwareness() error = %v, want ErrUserModelBlocked", err)
	}
	want := UserModelBlock{Platform: PlatformOpenAI, Model: "gpt-4.1"}
	if repo.lastBlock != want {
		t.Fatalf("load-aware composite block key = %+v, want %+v", repo.lastBlock, want)
	}
}

func TestOpenAIGatewaySchedulerChecksUserModelBlockBeforeSelection(t *testing.T) {
	blocks := &userModelBlockTestRepo{blocked: true}
	svc := &OpenAIGatewayService{userRepo: &userModelBlockUserRepo{blocks: blocks}}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))

	_, _, err := svc.SelectAccountWithScheduler(
		ctx,
		nil,
		"",
		"",
		"gpt-4.1",
		nil,
		OpenAIUpstreamTransportAny,
		false,
	)
	if !errors.Is(err, ErrUserModelBlocked) {
		t.Fatalf("SelectAccountWithScheduler() error = %v, want ErrUserModelBlocked", err)
	}
	want := UserModelBlock{Platform: PlatformOpenAI, Model: "gpt-4.1"}
	if blocks.lastUserID != 42 || blocks.lastBlock != want {
		t.Fatalf("OpenAI scheduler block check used %+v/%d, want %+v/42", blocks.lastBlock, blocks.lastUserID, want)
	}
}

func TestGatewayChecksUserModelBlockBeforeSelection(t *testing.T) {
	repo := &userModelBlockTestRepo{blocked: true}
	svc := &GatewayService{userModelBlockRepo: repo}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))

	err := svc.checkUserModelBlock(ctx, "openai", "gpt-4.1")
	if !errors.Is(err, ErrUserModelBlocked) {
		t.Fatalf("checkUserModelBlock() error = %v, want ErrUserModelBlocked", err)
	}
	if repo.lastUserID != 42 || repo.lastBlock != (UserModelBlock{Platform: "openai", Model: "gpt-4.1"}) {
		t.Fatalf("checkUserModelBlock() used %+v/%d, want exact key and user", repo.lastBlock, repo.lastUserID)
	}

	repo.blocked = false
	if err := svc.checkUserModelBlock(ctx, "openai", "gpt-4.1"); err != nil {
		t.Fatalf("unblocked model returned error: %v", err)
	}
	if err := svc.checkUserModelBlock(context.Background(), "openai", "gpt-4.1"); err != nil {
		t.Fatalf("anonymous context returned error: %v", err)
	}
}

func TestGatewayModelBlockCheckFailsClosedWhenRepositoryUnavailable(t *testing.T) {
	svc := &GatewayService{}
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(42))

	err := svc.checkUserModelBlock(ctx, "openai", "gpt-4.1")
	if !errors.Is(err, ErrUserModelBlockRepositoryUnavailable) {
		t.Fatalf("checkUserModelBlock() error = %v, want repository unavailable", err)
	}
	if err := svc.checkUserModelBlock(context.Background(), "openai", "gpt-4.1"); err != nil {
		t.Fatalf("anonymous context returned error: %v", err)
	}
}

type userModelBlockTestRepo struct {
	blocked    bool
	lastUserID int64
	lastBlock  UserModelBlock
}

func (r *userModelBlockTestRepo) ListUserModelBlocks(context.Context, int64) ([]UserModelBlock, error) {
	return nil, nil
}

func (r *userModelBlockTestRepo) SetUserModelBlock(context.Context, int64, UserModelBlock, bool) error {
	return nil
}

func (r *userModelBlockTestRepo) IsUserModelBlocked(_ context.Context, userID int64, block UserModelBlock) (bool, error) {
	r.lastUserID = userID
	r.lastBlock = block
	return r.blocked, nil
}

type userModelBlockUserRepo struct {
	UserRepository
	blocks *userModelBlockTestRepo
}

func (r *userModelBlockUserRepo) ListUserModelBlocks(ctx context.Context, userID int64) ([]UserModelBlock, error) {
	return r.blocks.ListUserModelBlocks(ctx, userID)
}

func (r *userModelBlockUserRepo) SetUserModelBlock(ctx context.Context, userID int64, block UserModelBlock, blocked bool) error {
	return r.blocks.SetUserModelBlock(ctx, userID, block, blocked)
}

func (r *userModelBlockUserRepo) IsUserModelBlocked(ctx context.Context, userID int64, block UserModelBlock) (bool, error) {
	return r.blocks.IsUserModelBlocked(ctx, userID, block)
}

type userModelBlockCacheProbe struct {
	GatewayCache
	getCalls int
}

func (c *userModelBlockCacheProbe) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	c.getCalls++
	return 0, ErrStickySessionNotFound
}
