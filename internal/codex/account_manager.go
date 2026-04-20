package codex

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/config"
)

type AccountManager struct {
	cfg      config.Config
	accounts *accounts.Service
	oauth    *OAuthService
	http     *HTTPClient

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewAccountManager(cfg config.Config, accountsSvc *accounts.Service, oauth *OAuthService, httpClient *HTTPClient) *AccountManager {
	return &AccountManager{
		cfg:      cfg,
		accounts: accountsSvc,
		oauth:    oauth,
		http:     httpClient,
		locks:    make(map[string]*sync.Mutex),
	}
}

func (m *AccountManager) AcquireReady(ctx context.Context, preferredID string) (accounts.Record, error) {
	record, err := m.accounts.Acquire(preferredID)
	if err != nil {
		return accounts.Record{}, err
	}
	return m.EnsureReady(ctx, record.ID)
}

func (m *AccountManager) EnsureReady(ctx context.Context, id string) (accounts.Record, error) {
	record, ok := m.accounts.Get(id)
	if !ok {
		return accounts.Record{}, fmt.Errorf("account not found")
	}
	if record.Status == accounts.StatusDisabled {
		return accounts.Record{}, fmt.Errorf("account disabled")
	}
	if record.Token.AccessToken == "" {
		return accounts.Record{}, fmt.Errorf("account has no access token")
	}
	if time.Until(record.Token.ExpiresAt) > m.cfg.RefreshSkew {
		return record, nil
	}

	lock := m.lockFor(id)
	lock.Lock()
	defer lock.Unlock()

	record, ok = m.accounts.Get(id)
	if !ok {
		return accounts.Record{}, fmt.Errorf("account not found")
	}
	if time.Until(record.Token.ExpiresAt) > m.cfg.RefreshSkew {
		return record, nil
	}

	nextToken, nextAccountID, err := m.oauth.Refresh(ctx, record.Token, record.AccountID)
	if err != nil {
		m.markRefreshFailure(id, err)
		return accounts.Record{}, err
	}
	if err := m.accounts.UpdateAuth(id, nextAccountID, nextToken); err != nil {
		return accounts.Record{}, err
	}
	updated, _ := m.accounts.Get(id)
	return updated, nil
}

func (m *AccountManager) Refresh(ctx context.Context, id string) (accounts.Record, error) {
	lock := m.lockFor(id)
	lock.Lock()
	defer lock.Unlock()

	record, ok := m.accounts.Get(id)
	if !ok {
		return accounts.Record{}, fmt.Errorf("account not found")
	}
	nextToken, nextAccountID, err := m.oauth.Refresh(ctx, record.Token, record.AccountID)
	if err != nil {
		m.markRefreshFailure(id, err)
		return accounts.Record{}, err
	}
	if err := m.accounts.UpdateAuth(id, nextAccountID, nextToken); err != nil {
		return accounts.Record{}, err
	}
	updated, _ := m.accounts.Get(id)
	return updated, nil
}

func (m *AccountManager) GetUsage(ctx context.Context, id string, cached bool) (accounts.Record, *accounts.QuotaSnapshot, error) {
	record, err := m.EnsureReady(ctx, id)
	if err != nil {
		return accounts.Record{}, nil, err
	}
	if cached && record.CachedQuota != nil {
		return record, record.CachedQuota, nil
	}

	_, quota, err := m.http.GetUsage(ctx, record)
	if err != nil {
		return record, nil, err
	}
	if err := m.accounts.UpdateQuota(record.ID, quota); err != nil {
		return record, nil, err
	}
	updated, _ := m.accounts.Get(record.ID)
	return updated, quota, nil
}

func (m *AccountManager) markRefreshFailure(id string, cause error) {
	if err := m.accounts.MarkError(id, accounts.StatusExpired, cause.Error()); err != nil {
		slog.Default().Error("persist account refresh failure status failed",
			"account_id", id,
			"refresh_error", cause.Error(),
			"error", err.Error(),
		)
	}
}

func (m *AccountManager) lockFor(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, ok := m.locks[id]
	if !ok {
		lock = &sync.Mutex{}
		m.locks[id] = lock
	}
	return lock
}
