package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUserRepositoryModelBlockReadsFailClosedWithoutSQL(t *testing.T) {
	repo := &userRepository{}
	block := service.UserModelBlock{Platform: service.PlatformOpenAI, Model: "gpt-4.1"}

	_, err := repo.IsUserModelBlocked(context.Background(), 42, block)
	if !errors.Is(err, service.ErrUserModelBlockRepositoryUnavailable) {
		t.Fatalf("IsUserModelBlocked() error = %v, want repository unavailable", err)
	}

	_, err = repo.ListUserModelBlocks(context.Background(), 42)
	if !errors.Is(err, service.ErrUserModelBlockRepositoryUnavailable) {
		t.Fatalf("ListUserModelBlocks() error = %v, want repository unavailable", err)
	}
}
