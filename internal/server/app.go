package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/admin"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/codex/wsclient"
	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/middleware"
	"chatgpt-codex-proxy/internal/store"
)

type App struct {
	cfg           config.Config
	logger        *slog.Logger
	engine        *gin.Engine
	accounts      *accounts.Service
	deviceLogins  *admin.DeviceLoginService
	accountMgr    *codex.AccountManager
	httpClient    *codex.HTTPClient
	wsClient      *wsclient.Client
	continuations *accounts.ContinuationManager
	cancel        context.CancelFunc
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	accountsStore := store.NewJSONAccountsStore(cfg.DataDir)
	accountsSvc, err := accounts.NewService(accountsStore, accounts.RotationStrategy(cfg.RotationStrategy), accounts.ServiceOptions{
		RateLimitFallback: cfg.RateLimitFallback,
		QuotaFallback:     cfg.QuotaFallback,
	})
	if err != nil {
		return nil, err
	}

	httpClient := codex.NewHTTPClient(cfg)
	oauthSvc := codex.NewOAuthService(cfg)
	accountMgr := codex.NewAccountManager(cfg, accountsSvc, oauthSvc, httpClient)
	deviceLogins := admin.NewDeviceLoginService(oauthSvc, accountsSvc, cfg.LoginTimeout)

	engine := gin.New()
	engine.SetTrustedProxies(nil)
	engine.Use(middleware.RequestID())
	engine.Use(middleware.RequestLogger(logger, middleware.RequestLoggerOptions{
		SkipPaths: map[string]struct{}{
			"/health/live": {},
		},
	}))
	engine.Use(middleware.Recovery(logger))

	app := &App{
		cfg:           cfg,
		logger:        logger,
		engine:        engine,
		accounts:      accountsSvc,
		deviceLogins:  deviceLogins,
		accountMgr:    accountMgr,
		httpClient:    httpClient,
		wsClient:      wsclient.New(),
		continuations: accounts.NewContinuationManager(cfg.ContinuationTTL),
	}
	app.routes()

	ctx, cancel := context.WithCancel(context.Background())
	app.cancel = cancel
	go app.housekeeping(ctx)

	return app, nil
}

func (a *App) Handler() http.Handler {
	return a.engine
}

func (a *App) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	return a.httpClient.Close()
}

func (a *App) housekeeping(ctx context.Context) {
	sweepTicker := time.NewTicker(30 * time.Second)
	defer sweepTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweepTicker.C:
			a.continuations.Sweep()
			a.deviceLogins.DeleteExpired(time.Now().UTC())
		}
	}
}

func (a *App) routes() {
	a.engine.GET("/health/live", a.handleHealthLive)

	protected := a.engine.Group("/")
	protected.Use(middleware.APIKey(a.cfg.ProxyAPIKey))
	protected.GET("/health", a.handleHealth)
	protected.GET("/v1/models", a.handleModels)
	protected.GET("/v1/models/:model_id", a.handleModelByID)
	protected.POST("/v1/chat/completions", a.handleChatCompletions)
	protected.POST("/v1/responses", a.handleResponses)

	adminGroup := protected.Group("/admin")
	adminGroup.GET("/accounts", a.handleAdminAccounts)
	adminGroup.POST("/accounts/device-login/start", a.handleAdminDeviceLoginStart)
	adminGroup.GET("/accounts/device-login/:login_id", a.handleAdminDeviceLoginGet)
	adminGroup.DELETE("/accounts/:account_id", a.handleAdminAccountDelete)
	adminGroup.PATCH("/accounts/:account_id", a.handleAdminAccountPatch)
	adminGroup.GET("/accounts/:account_id/usage", a.handleAdminAccountUsage)
	adminGroup.POST("/accounts/:account_id/refresh", a.handleAdminAccountRefresh)
	adminGroup.GET("/rotation", a.handleAdminRotationGet)
	adminGroup.PUT("/rotation", a.handleAdminRotationPut)
}

func (a *App) writeOpenAIError(c *gin.Context, status int, code, message, errType string) {
	middleware.AbortJSON(c, status, middleware.OpenAIErrorPayload(message, errType, code, ""))
}

func (a *App) writeAdminError(c *gin.Context, status int, code, message string) {
	middleware.AbortJSON(c, status, middleware.AdminErrorPayload(code, message))
}

func (a *App) classifyUpstreamError(accountID string, err error) (int, string, string) {
	var upstreamErr *codex.UpstreamError
	if errors.As(err, &upstreamErr) {
		text := strings.ToLower(upstreamErr.Message())
		switch upstreamErr.StatusCode {
		case http.StatusUnauthorized:
			a.markAccountError(accountID, accounts.StatusExpired, err)
			return http.StatusUnauthorized, "upstream_unauthorized", "upstream account unauthorized"
		case http.StatusForbidden:
			if !looksLikeCloudflareBlock(text) {
				a.markAccountError(accountID, accounts.StatusBanned, err)
				return http.StatusForbidden, "account_banned", "upstream account banned or deactivated"
			}
		case http.StatusPaymentRequired:
			a.setAccountCooldown(accountID, a.quotaCooldownUntil(accountID, time.Now().UTC()), err)
			return http.StatusPaymentRequired, "quota_exhausted", "upstream account quota exhausted"
		case http.StatusTooManyRequests:
			a.setAccountCooldown(accountID, a.rateLimitCooldownUntil(accountID, err, time.Now().UTC()), err)
			return http.StatusTooManyRequests, "rate_limited", "upstream account rate limited"
		}
		return clampUpstreamStatus(upstreamErr.StatusCode), "upstream_error", upstreamErr.Message()
	}
	return http.StatusBadGateway, "upstream_error", err.Error()
}

func (a *App) markAccountError(accountID string, status accounts.Status, cause error) {
	if err := a.accounts.MarkError(accountID, status, cause.Error()); err != nil {
		a.logger.Error("persist account error status failed",
			"account_id", accountID,
			"status", string(status),
			"error", err.Error(),
		)
	}
}

func (a *App) setAccountCooldown(accountID string, until *time.Time, cause error) {
	if strings.TrimSpace(accountID) == "" {
		return
	}
	if err := a.accounts.SetCooldown(accountID, until, cause.Error()); err != nil {
		a.logger.Error("persist account cooldown failed",
			"account_id", accountID,
			"error", err.Error(),
		)
	}
}

func (a *App) rateLimitCooldownUntil(accountID string, cause error, now time.Time) *time.Time {
	if retryAfter := retryAfterFromError(cause); retryAfter > 0 {
		return fallbackUntil(now, retryAfter)
	}
	if record, ok := a.accounts.Get(accountID); ok {
		if reset := primaryQuotaReset(record.CachedQuota, now); reset != nil {
			return reset
		}
	}
	return fallbackUntil(now, a.accounts.RateLimitFallback())
}

func (a *App) quotaCooldownUntil(accountID string, now time.Time) *time.Time {
	if record, ok := a.accounts.Get(accountID); ok {
		if reset := quotaResetForCooldown(record.CachedQuota, now); reset != nil {
			return reset
		}
	}
	return fallbackUntil(now, a.accounts.QuotaFallback())
}

func retryAfterFromError(err error) time.Duration {
	if err == nil {
		return 0
	}
	var upstreamErr *codex.UpstreamError
	if errors.As(err, &upstreamErr) && upstreamErr.RetryAfter > 0 {
		return time.Duration(upstreamErr.RetryAfter) * time.Second
	}
	text := strings.ToLower(err.Error())
	idx := strings.Index(text, "retry-after")
	if idx < 0 {
		return 0
	}
	fragment := text[idx:]
	digits := strings.Builder{}
	seenDigits := false
	for _, ch := range fragment {
		if ch >= '0' && ch <= '9' {
			digits.WriteRune(ch)
			seenDigits = true
			continue
		}
		if seenDigits {
			break
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	seconds, err := strconv.Atoi(digits.String())
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func clampUpstreamStatus(status int) int {
	if status >= 400 && status < 600 {
		return status
	}
	return http.StatusBadGateway
}

func looksLikeCloudflareBlock(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "cf_chl") ||
		strings.Contains(normalized, "<!doctype") ||
		strings.Contains(normalized, "<html")
}

func primaryQuotaReset(snapshot *accounts.QuotaSnapshot, now time.Time) *time.Time {
	if snapshot == nil {
		return nil
	}
	return activeWindowReset(&snapshot.RateLimit, now)
}

func quotaResetForCooldown(snapshot *accounts.QuotaSnapshot, now time.Time) *time.Time {
	if snapshot == nil {
		return nil
	}
	if reset := activeWindowReset(&snapshot.RateLimit, now); reset != nil {
		return reset
	}
	if reset := activeWindowReset(snapshot.SecondaryRateLimit, now); reset != nil {
		return reset
	}
	return nil
}

func activeWindowReset(window *accounts.RateLimitWindow, now time.Time) *time.Time {
	if window == nil {
		return nil
	}
	if !window.Allowed {
		if window.ResetAt == nil {
			return nil
		}
		if window.ResetAt.After(now) {
			ts := window.ResetAt.UTC()
			return &ts
		}
		return nil
	}
	if !window.LimitReached || window.ResetAt == nil {
		return nil
	}
	if !window.ResetAt.After(now) {
		return nil
	}
	ts := window.ResetAt.UTC()
	return &ts
}

func fallbackUntil(now time.Time, duration time.Duration) *time.Time {
	if duration <= 0 {
		return nil
	}
	until := now.Add(duration).UTC()
	return &until
}

func writeSSE(w io.Writer, eventName string, payload []byte) {
	if eventName != "" {
		_, _ = io.WriteString(w, "event: "+eventName+"\n")
	}
	_, _ = io.WriteString(w, "data: ")
	_, _ = w.Write(payload)
	_, _ = io.WriteString(w, "\n\n")
}
