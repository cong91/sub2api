package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// isDeviceLoginCodePrefix checks if the code starts with the DLG- prefix.
func isDeviceLoginCodePrefix(code string) bool {
	return strings.HasPrefix(strings.ToUpper(code), "DLG-")
}

// completeDeviceInviteLoginByDevice handles device login using the new device_code path
// (no redeem_codes dependency). This is the Phase 2 dual-write path.
func (s *AuthService) completeDeviceInviteLoginByDevice(ctx context.Context, input InviteLoginInput, device *UserDevice) (*InviteLoginResult, error) {
	if device == nil {
		return nil, ErrInvitationCodeInvalid
	}
	if s.userRepo == nil {
		return nil, ErrServiceUnavailable
	}

	deviceHash := normalizeDeviceHash(input.DeviceHash)
	clientKind := strings.TrimSpace(strings.ToLower(input.ClientKind))
	allowWebLoginWithoutDeviceHash := deviceHash == "" && clientKind == "web"
	if deviceHash == "" && !allowWebLoginWithoutDeviceHash {
		return nil, ErrDeviceHashRequired
	}
	if deviceHash != "" && len(deviceHash) != 64 {
		return nil, ErrDeviceHashInvalid
	}
	for _, ch := range deviceHash {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return nil, ErrDeviceHashInvalid
		}
	}

	if strings.TrimSpace(device.Status) != UserDeviceStatusActive {
		return nil, ErrDeviceRevoked
	}
	if deviceHash != "" && normalizeDeviceHash(device.DeviceHash) != deviceHash {
		return nil, ErrDeviceMismatch
	}

	inputInstallID := strings.TrimSpace(input.InstallID)
	if device.InstallID != nil {
		boundInstallID := strings.TrimSpace(*device.InstallID)
		if boundInstallID != "" && (inputInstallID == "" || !strings.EqualFold(boundInstallID, inputInstallID)) {
			logger.LegacyPrintf("service.auth", "[DeviceInviteLogin] install_id missing or changed for matching device_hash: user_device_id=%d user_id=%d", device.ID, device.UserID)
		}
	}

	user, err := s.userRepo.GetByID(ctx, device.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvitationCodeInvalid
		}
		return nil, ErrServiceUnavailable
	}
	if user.Status == StatusPendingActivation && !allowWebLoginWithoutDeviceHash {
		return nil, ErrDeviceActivationPending
	}
	if !user.IsActive() && !allowWebLoginWithoutDeviceHash {
		return nil, ErrUserNotActive
	}

	var bootstrapKeys []InviteBootstrapAPIKey
	if !allowWebLoginWithoutDeviceHash {
		// For the new path, we need the redeem code to provision API keys.
		// Fetch it via the still-existing FK for backward compat.
		code, codeErr := s.redeemRepo.GetByID(ctx, device.LoginRedeemCodeID)
		if codeErr != nil {
			return nil, ErrServiceUnavailable
		}
		bootstrapKeys, err = s.provisionInviteBootstrapAPIKeys(ctx, user.ID, code)
		if err != nil {
			return nil, err
		}
	}

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, fmt.Errorf("generate token pair: %w", err)
	}

	if err := s.inviteLoginDeviceRepo.UpdateLastLoginAt(ctx, device.ID, time.Now().UTC()); err != nil {
		return nil, ErrServiceUnavailable
	}

	return &InviteLoginResult{
		TokenPair:        tokenPair,
		User:             user,
		BootstrapAPIKeys: bootstrapKeys,
	}, nil
}

func (s *AuthService) completeDeviceInviteLogin(ctx context.Context, input InviteLoginInput, code *RedeemCode) (*InviteLoginResult, error) {
	if code == nil || code.Type != RedeemTypeDeviceLogin {
		return nil, ErrInvitationCodeInvalid
	}
	if s.inviteLoginDeviceRepo == nil || s.userRepo == nil {
		return nil, ErrServiceUnavailable
	}

	deviceHash := normalizeDeviceHash(input.DeviceHash)
	clientKind := strings.TrimSpace(strings.ToLower(input.ClientKind))
	allowWebLoginWithoutDeviceHash := deviceHash == "" && clientKind == "web"
	if deviceHash == "" && !allowWebLoginWithoutDeviceHash {
		return nil, ErrDeviceHashRequired
	}
	if deviceHash != "" && len(deviceHash) != 64 {
		return nil, ErrDeviceHashInvalid
	}
	for _, ch := range deviceHash {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return nil, ErrDeviceHashInvalid
		}
	}

	device, err := s.inviteLoginDeviceRepo.GetByLoginRedeemCodeID(ctx, code.ID)
	if err != nil {
		if errors.Is(err, ErrUserDeviceNotFound) {
			return nil, ErrInvitationCodeInvalid
		}
		return nil, ErrServiceUnavailable
	}
	if device == nil {
		return nil, ErrDeviceRevoked
	}
	if strings.TrimSpace(device.Status) != UserDeviceStatusActive {
		return nil, ErrDeviceRevoked
	}
	if deviceHash != "" && normalizeDeviceHash(device.DeviceHash) != deviceHash {
		return nil, ErrDeviceMismatch
	}

	inputInstallID := strings.TrimSpace(input.InstallID)
	if device.InstallID != nil {
		boundInstallID := strings.TrimSpace(*device.InstallID)
		if boundInstallID != "" && (inputInstallID == "" || !strings.EqualFold(boundInstallID, inputInstallID)) {
			logger.LegacyPrintf("service.auth", "[DeviceInviteLogin] install_id missing or changed for matching device_hash: user_device_id=%d user_id=%d", device.ID, device.UserID)
		}
	}

	user, err := s.userRepo.GetByID(ctx, device.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvitationCodeInvalid
		}
		return nil, ErrServiceUnavailable
	}
	if user.Status == StatusPendingActivation && !allowWebLoginWithoutDeviceHash {
		return nil, ErrDeviceActivationPending
	}
	if !user.IsActive() && !allowWebLoginWithoutDeviceHash {
		return nil, ErrUserNotActive
	}

	var bootstrapKeys []InviteBootstrapAPIKey
	if !allowWebLoginWithoutDeviceHash {
		bootstrapKeys, err = s.provisionInviteBootstrapAPIKeys(ctx, user.ID, code)
		if err != nil {
			return nil, err
		}
	}

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, fmt.Errorf("generate token pair: %w", err)
	}

	if err := s.inviteLoginDeviceRepo.UpdateLastLoginAt(ctx, device.ID, time.Now().UTC()); err != nil {
		return nil, ErrServiceUnavailable
	}

	return &InviteLoginResult{
		TokenPair:        tokenPair,
		User:             user,
		BootstrapAPIKeys: bootstrapKeys,
	}, nil
}
