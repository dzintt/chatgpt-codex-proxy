package admin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
)

type DeviceLoginService struct {
	mu       sync.RWMutex
	oauth    *codex.OAuthService
	accounts *accounts.Service
	timeout  time.Duration
	logins   map[string]*pendingLogin
}

type pendingLogin struct {
	accounts.DeviceLoginRecord
	DeviceAuthID string
	Interval     time.Duration
}

func NewDeviceLoginService(oauth *codex.OAuthService, accountsSvc *accounts.Service, timeout time.Duration) *DeviceLoginService {
	return &DeviceLoginService{
		oauth:    oauth,
		accounts: accountsSvc,
		timeout:  timeout,
		logins:   make(map[string]*pendingLogin),
	}
}

func (s *DeviceLoginService) Start(ctx context.Context) (accounts.DeviceLoginRecord, error) {
	resp, err := s.oauth.RequestDeviceCode(ctx)
	if err != nil {
		return accounts.DeviceLoginRecord{}, err
	}

	now := time.Now().UTC()
	login := &pendingLogin{
		DeviceLoginRecord: accounts.DeviceLoginRecord{
			LoginID:   "login_" + now.Format("20060102150405.000000000"),
			AuthURL:   s.oauth.DeviceAuthURL(),
			UserCode:  resp.UserCode,
			Status:    "pending",
			CreatedAt: now,
			ExpiresAt: now.Add(s.timeout),
		},
		DeviceAuthID: resp.DeviceAuthID,
		Interval:     time.Duration(maxInt(resp.Interval, 5)) * time.Second,
	}

	s.mu.Lock()
	s.logins[login.LoginID] = login
	s.mu.Unlock()

	go s.poll(login)

	return login.DeviceLoginRecord, nil
}

func (s *DeviceLoginService) Get(loginID string) (accounts.DeviceLoginRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	login, ok := s.logins[loginID]
	if !ok {
		return accounts.DeviceLoginRecord{}, false
	}
	return login.DeviceLoginRecord, true
}

func (s *DeviceLoginService) poll(login *pendingLogin) {
	ctx, cancel := context.WithDeadline(context.Background(), login.ExpiresAt)
	defer cancel()

	ticker := time.NewTicker(login.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.update(login.LoginID, func(target *pendingLogin) {
				if target.Status == "pending" {
					target.Status = "expired"
					target.Error = "device login expired"
				}
			})
			return
		case <-ticker.C:
			result, err := s.oauth.PollDeviceCode(ctx, login.DeviceAuthID, login.UserCode)
			if err != nil {
				if isAuthorizationPending(err) {
					continue
				}
				s.update(login.LoginID, func(target *pendingLogin) {
					target.Status = "error"
					target.Error = err.Error()
				})
				return
			}

			if result == nil {
				continue
			}

			token, accountID, err := s.oauth.ExchangeAuthorizationCode(ctx, result.AuthorizationCode, result.CodeVerifier)
			if err != nil {
				s.update(login.LoginID, func(target *pendingLogin) {
					target.Status = "error"
					target.Error = err.Error()
				})
				return
			}
			if strings.TrimSpace(accountID) == "" {
				s.update(login.LoginID, func(target *pendingLogin) {
					target.Status = "error"
					target.Error = "oauth exchange did not return account_id"
				})
				return
			}

			if _, err := s.accounts.UpsertFromToken(accountID, token); err != nil {
				s.update(login.LoginID, func(target *pendingLogin) {
					target.Status = "error"
					target.Error = err.Error()
				})
				return
			}

			s.update(login.LoginID, func(target *pendingLogin) {
				target.Status = "ready"
				target.Error = ""
			})
			return
		}
	}
}

func (s *DeviceLoginService) update(loginID string, fn func(*pendingLogin)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.logins[loginID]
	if !ok {
		return
	}
	fn(login)
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func isAuthorizationPending(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "authorization_pending") || strings.Contains(text, "not found")
}

func (s *DeviceLoginService) DeleteExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, login := range s.logins {
		if login.ExpiresAt.Before(now) && login.Status != "pending" {
			delete(s.logins, id)
		}
	}
}

func (s *DeviceLoginService) ForceStatus(loginID, status, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.logins[loginID]
	if !ok {
		return fmt.Errorf("login not found")
	}
	login.Status = status
	login.Error = message
	return nil
}
