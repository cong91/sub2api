package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	maxUserModelBlockPlatformLength = 50
	maxUserModelBlockModelLength    = 255
)

var (
	ErrUserModelBlockInvalid = infraerrors.BadRequest(
		"USER_MODEL_BLOCK_INVALID",
		"platform and model are required and must be within the allowed length",
	)
	ErrUserModelBlocked                    = infraerrors.Forbidden("MODEL_BLOCKED", "model is blocked for this user")
	ErrUserModelBlockRepositoryUnavailable = infraerrors.ServiceUnavailable(
		"USER_MODEL_BLOCK_REPOSITORY_UNAVAILABLE",
		"user model block repository is unavailable",
	)
)

// UserModelBlock is an exact user preference for a provider platform/model pair.
// The pair is intentionally not resolved through aliases or provider capability metadata.
type UserModelBlock struct {
	Platform string `json:"platform"`
	Model    string `json:"model"`
}

// UserModelBlockRepository is optional so existing UserRepository test doubles do not
// need to implement this feature. The production user repository implements it.
type UserModelBlockRepository interface {
	ListUserModelBlocks(ctx context.Context, userID int64) ([]UserModelBlock, error)
	SetUserModelBlock(ctx context.Context, userID int64, block UserModelBlock, blocked bool) error
	IsUserModelBlocked(ctx context.Context, userID int64, block UserModelBlock) (bool, error)
}

func userModelBlockRepositoryFromUserRepository(repo UserRepository) UserModelBlockRepository {
	if repo == nil {
		return nil
	}
	blockRepo, _ := repo.(UserModelBlockRepository)
	return blockRepo
}

func NormalizeUserModelBlock(platform, model string) (UserModelBlock, error) {
	block := UserModelBlock{
		Platform: strings.TrimSpace(platform),
		Model:    strings.TrimSpace(model),
	}
	if block.Platform == "" || block.Model == "" ||
		len(block.Platform) > maxUserModelBlockPlatformLength ||
		len(block.Model) > maxUserModelBlockModelLength {
		return UserModelBlock{}, ErrUserModelBlockInvalid
	}
	return block, nil
}
