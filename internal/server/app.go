package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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
	accountsSvc, err := accounts.NewService(accountsStore, accounts.RotationStrategy(cfg.RotationStrategy))
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
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
}

func (a *App) writeOpenAIError(c *gin.Context, status int, code, message, errType string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

func (a *App) writeAdminError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error":   code,
		"message": message,
	})
}

func (a *App) classifyUpstreamError(accountID string, err error) (int, string, string) {
	if err == nil {
		return http.StatusBadGateway, "upstream_error", "upstream error"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "401"), strings.Contains(text, "unauthorized"):
		_ = a.accounts.MarkError(accountID, accounts.StatusExpired, err.Error())
		return http.StatusUnauthorized, "upstream_unauthorized", "upstream account unauthorized"
	case strings.Contains(text, "402"), strings.Contains(text, "payment"), strings.Contains(text, "quota"):
		_ = a.accounts.MarkError(accountID, accounts.StatusQuotaExhausted, err.Error())
		return http.StatusPaymentRequired, "quota_exhausted", "upstream account quota exhausted"
	case strings.Contains(text, "429"), strings.Contains(text, "rate"):
		_ = a.accounts.MarkError(accountID, accounts.StatusRateLimited, err.Error())
		return http.StatusTooManyRequests, "rate_limited", "upstream account rate limited"
	default:
		return http.StatusBadGateway, "upstream_error", err.Error()
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
