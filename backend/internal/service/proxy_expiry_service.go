package service

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	SettingTelegramNotifyProxyExpiringThresholdDays = "telegram_notify_proxy_expiring_threshold_days"
	defaultProxyExpiringThresholdDays               = 3
)

// ProxyExpiryService periodically checks for proxies nearing expiration and sends Telegram notifications.
type ProxyExpiryService struct {
	proxyRepo         ProxyRepository
	settingRepo       SettingRepository
	interval          time.Duration
	stopCh            chan struct{}
	stopOnce          sync.Once
	wg                sync.WaitGroup
	telegramNotifySvc *TelegramNotifyService
}

func NewProxyExpiryService(proxyRepo ProxyRepository, settingRepo SettingRepository, interval time.Duration) *ProxyExpiryService {
	return &ProxyExpiryService{
		proxyRepo:   proxyRepo,
		settingRepo: settingRepo,
		interval:    interval,
		stopCh:      make(chan struct{}),
	}
}

func (s *ProxyExpiryService) SetTelegramNotifyService(svc *TelegramNotifyService) {
	s.telegramNotifySvc = svc
}

func (s *ProxyExpiryService) Start() {
	if s == nil || s.proxyRepo == nil || s.interval <= 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		// Initial delay to avoid startup burst
		time.Sleep(30 * time.Second)
		s.runOnce()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ProxyExpiryService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.wg.Wait()
}

func (s *ProxyExpiryService) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: Deactivate expired proxies (always runs regardless of Telegram config)
	deactivated, err := s.proxyRepo.DeactivateExpired(ctx, time.Now())
	if err != nil {
		slog.Error("[ProxyExpiry] Failed to deactivate expired proxies", "error", err)
	} else if len(deactivated) > 0 {
		slog.Info("[ProxyExpiry] Deactivated expired proxies", "count", len(deactivated))
		// Notify via Telegram if configured
		if s.telegramNotifySvc != nil {
			for _, p := range deactivated {
				if p.ExpiresAt != nil {
					go s.telegramNotifySvc.NotifyProxyExpired(context.Background(), p.Name, *p.ExpiresAt, true)
				}
			}
		}
	}

	// Step 2: Warn about proxies nearing expiration (only if Telegram is configured)
	if s.telegramNotifySvc == nil {
		return
	}
	if !s.telegramNotifySvc.isEnabled(context.Background(), SettingTelegramNotifyProxyExpired) {
		return
	}

	thresholdDays := s.getThresholdDays(ctx)
	deadline := time.Now().Add(time.Duration(thresholdDays) * 24 * time.Hour)

	proxies, err := s.proxyRepo.ListExpiringBefore(ctx, deadline)
	if err != nil {
		slog.Error("[ProxyExpiry] Failed to list expiring proxies", "error", err)
		return
	}

	if len(proxies) == 0 {
		return
	}

	go s.telegramNotifySvc.NotifyProxyExpiring(context.Background(), proxies, thresholdDays)
}

func (s *ProxyExpiryService) getThresholdDays(ctx context.Context) int {
	if s.settingRepo == nil {
		return defaultProxyExpiringThresholdDays
	}
	val, err := s.settingRepo.GetValue(ctx, SettingTelegramNotifyProxyExpiringThresholdDays)
	if err != nil || val == "" {
		return defaultProxyExpiringThresholdDays
	}
	days := 0
	for _, c := range val {
		if c >= '0' && c <= '9' {
			days = days*10 + int(c-'0')
		} else {
			break
		}
	}
	if days <= 0 {
		return defaultProxyExpiringThresholdDays
	}
	return days
}
