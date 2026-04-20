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
	usageHistory  *accounts.UsageHistoryStore
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
	usageHistory, err := accounts.NewUsageHistoryStore(cfg.DataDir, cfg.UsageHistoryRetention)
	if err != nil {
		return nil, err
	}
	if err := usageHistory.RecoverBaseline(accountsSvc.Summary()); err != nil {
		logger.Warn("recover usage history baseline failed", "error", err.Error())
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
		usageHistory:  usageHistory,
	}
	app.routes()
	app.recordUsageSnapshot(time.Now().UTC())

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

	var snapshotTicker *time.Ticker
	var snapshotC <-chan time.Time
	if a.cfg.UsageSnapshotInterval > 0 {
		snapshotTicker = time.NewTicker(a.cfg.UsageSnapshotInterval)
		snapshotC = snapshotTicker.C
		defer snapshotTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweepTicker.C:
			a.continuations.Sweep()
			a.deviceLogins.DeleteExpired(time.Now().UTC())
		case <-snapshotC:
			a.recordUsageSnapshot(time.Now().UTC())
		}
	}
}

func (a *App) recordUsageSnapshot(now time.Time) {
	if a.usageHistory == nil {
		return
	}
	if err := a.usageHistory.RecordSummary(a.accounts.Summary(), now); err != nil {
		a.logger.Error("record usage history failed", "error", err.Error())
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
	adminGroup.GET("/usage/summary", a.handleAdminUsageSummary)
	adminGroup.GET("/usage/history", a.handleAdminUsageHistory)
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
			if strings.Contains(text, "banned") || strings.Contains(text, "deactivated") || strings.Contains(text, "suspended") {
				a.markAccountError(accountID, accounts.StatusBanned, err)
				return http.StatusForbidden, "account_banned", "upstream account banned or deactivated"
			}
		case http.StatusPaymentRequired:
			reason := a.quotaBlockReason(accountID)
			a.markAccountBlocked(accountID, reason, a.blockUntil(accountID, reason, err), err)
			return http.StatusPaymentRequired, "quota_exhausted", "upstream account quota exhausted"
		case http.StatusTooManyRequests:
			a.recordRateLimitedAttempt(accountID)
			a.markAccountBlocked(accountID, accounts.BlockRateLimit, a.blockUntil(accountID, accounts.BlockRateLimit, err), err)
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

func (a *App) markAccountBlocked(accountID string, reason accounts.BlockReason, until *time.Time, cause error) {
	if strings.TrimSpace(accountID) == "" {
		return
	}
	if err := a.accounts.MarkBlocked(accountID, reason, until, cause.Error()); err != nil {
		a.logger.Error("persist account block failed",
			"account_id", accountID,
			"reason", string(reason),
			"error", err.Error(),
		)
	}
}

func (a *App) quotaBlockReason(accountID string) accounts.BlockReason {
	record, ok := a.accounts.Get(accountID)
	if !ok || record.CachedQuota == nil {
		return accounts.BlockQuotaPrimary
	}
	now := time.Now().UTC()
	if exhaustedQuotaWindow(record.CachedQuota.SecondaryRateLimit, now) {
		return accounts.BlockQuotaSecondary
	}
	if exhaustedQuotaWindow(record.CachedQuota.CodeReviewRateLimit, now) {
		return accounts.BlockCodeReview
	}
	return accounts.BlockQuotaPrimary
}

func (a *App) blockUntil(accountID string, reason accounts.BlockReason, cause error) *time.Time {
	now := time.Now().UTC()
	fallback := fallbackUntil(now, a.fallbackDuration(reason))

	if strings.TrimSpace(accountID) == "" {
		return fallback
	}

	var cached *time.Time
	record, ok := a.accounts.Get(accountID)
	if ok && record.CachedQuota != nil {
		if reason == accounts.BlockRateLimit {
			cached = quotaResetForReason(record.CachedQuota, accounts.BlockQuotaPrimary, now)
		} else {
			cached = quotaResetForReason(record.CachedQuota, reason, now)
		}
	}

	if reason == accounts.BlockRateLimit {
		retry := (*time.Time)(nil)
		if retryAfter := retryAfterFromError(cause); retryAfter > 0 {
			retry = fallbackUntil(now, retryAfter)
		}
		if retry != nil || cached != nil {
			return laterTime(retry, cached)
		}
		return fallback
	}

	if cached != nil {
		return cached
	}
	return nil
}

func (a *App) fallbackDuration(reason accounts.BlockReason) time.Duration {
	if reason == accounts.BlockRateLimit {
		return a.cfg.RateLimitFallback
	}
	return a.cfg.QuotaFallback
}

func retryAfterFromError(err error) time.Duration {
	if err == nil {
		return 0
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

func quotaResetForReason(snapshot *accounts.QuotaSnapshot, reason accounts.BlockReason, now time.Time) *time.Time {
	if snapshot == nil {
		return nil
	}
	switch reason {
	case accounts.BlockQuotaSecondary:
		return activeWindowReset(snapshot.SecondaryRateLimit, now)
	case accounts.BlockCodeReview:
		return activeWindowReset(snapshot.CodeReviewRateLimit, now)
	case accounts.BlockRateLimit, accounts.BlockQuotaPrimary:
		return activeWindowReset(&snapshot.RateLimit, now)
	default:
		return nil
	}
}

func exhaustedQuotaWindow(window *accounts.RateLimitWindow, now time.Time) bool {
	if window == nil || !window.LimitReached {
		return false
	}
	if window.ResetAt == nil {
		return true
	}
	return window.ResetAt.After(now)
}

func activeWindowReset(window *accounts.RateLimitWindow, now time.Time) *time.Time {
	if window == nil || !window.LimitReached {
		return nil
	}
	if window.ResetAt == nil {
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

func laterTime(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.After(*b):
		return a
	default:
		return b
	}
}

func writeSSE(w io.Writer, eventName string, payload []byte) {
	if eventName != "" {
		_, _ = io.WriteString(w, "event: "+eventName+"\n")
	}
	_, _ = io.WriteString(w, "data: ")
	_, _ = w.Write(payload)
	_, _ = io.WriteString(w, "\n\n")
}
