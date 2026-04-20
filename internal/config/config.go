package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultListenAddr            = ":8080"
	defaultDataDir               = "data"
	defaultDefaultModel          = "gpt-5.3-codex"
	defaultOriginator            = "Codex Desktop"
	defaultOpenAIBeta            = "responses_websockets=2026-02-06"
	defaultResidency             = "us"
	defaultRotationStrategy      = "least_used"
	defaultCodexBaseURL          = "https://chatgpt.com/backend-api"
	defaultAuthIssuer            = "https://auth.openai.com"
	defaultClientID              = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultLoginTimeoutSeconds   = 900
	defaultUsageCacheTTLSeconds  = 20
	defaultContinuationTTLMinute = 60
	defaultRequestTimeoutSecond  = 120
	defaultUsageSnapshotMinutes  = 5
	defaultHistoryRetentionDays  = 7
	defaultRateLimitFallbackSec  = 60
	defaultQuotaFallbackSec      = 300
)

type Config struct {
	ListenAddr            string
	DataDir               string
	ProxyAPIKey           string
	DefaultModel          string
	Originator            string
	OpenAIBeta            string
	Residency             string
	RotationStrategy      string
	CodexBaseURL          string
	AuthIssuer            string
	OAuthClientID         string
	LoginTimeout          time.Duration
	UsageCacheTTL         time.Duration
	ContinuationTTL       time.Duration
	RequestTimeout        time.Duration
	RefreshSkew           time.Duration
	UsageSnapshotInterval time.Duration
	UsageHistoryRetention time.Duration
	RateLimitFallback     time.Duration
	QuotaFallback         time.Duration
	LogLevel              slogLevel
	UserAgentTemplate     string
	ChromiumVersion       string
	Platform              string
	ClientHintPlatform    string
	Arch                  string
	HeaderOrder           []string
	DefaultAcceptLanguage string
}

type slogLevel string

func Load() (Config, error) {
	_ = godotenv.Load()

	dataDir := envOr("DATA_DIR", defaultDataDir)
	if !filepath.IsAbs(dataDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd: %w", err)
		}
		dataDir = filepath.Join(cwd, dataDir)
	}

	cfg := Config{
		ListenAddr:            envOr("LISTEN_ADDR", defaultListenAddr),
		DataDir:               dataDir,
		ProxyAPIKey:           strings.TrimSpace(os.Getenv("PROXY_API_KEY")),
		DefaultModel:          envOr("DEFAULT_MODEL", defaultDefaultModel),
		Originator:            envOr("CODEX_ORIGINATOR", defaultOriginator),
		OpenAIBeta:            envOr("CODEX_OPENAI_BETA", defaultOpenAIBeta),
		Residency:             envOr("CODEX_RESIDENCY", defaultResidency),
		RotationStrategy:      envOr("ROTATION_STRATEGY", defaultRotationStrategy),
		CodexBaseURL:          envOr("CODEX_BASE_URL", defaultCodexBaseURL),
		AuthIssuer:            envOr("OPENAI_AUTH_ISSUER", defaultAuthIssuer),
		OAuthClientID:         envOr("OPENAI_OAUTH_CLIENT_ID", defaultClientID),
		LoginTimeout:          time.Duration(envInt("LOGIN_TIMEOUT_SECONDS", defaultLoginTimeoutSeconds)) * time.Second,
		UsageCacheTTL:         time.Duration(envInt("USAGE_CACHE_TTL_SECONDS", defaultUsageCacheTTLSeconds)) * time.Second,
		ContinuationTTL:       time.Duration(envInt("CONTINUATION_TTL_MINUTES", defaultContinuationTTLMinute)) * time.Minute,
		RequestTimeout:        time.Duration(envInt("REQUEST_TIMEOUT_SECONDS", defaultRequestTimeoutSecond)) * time.Second,
		RefreshSkew:           60 * time.Second,
		UsageSnapshotInterval: time.Duration(envInt("USAGE_SNAPSHOT_INTERVAL_MINUTES", defaultUsageSnapshotMinutes)) * time.Minute,
		UsageHistoryRetention: time.Duration(envInt("USAGE_HISTORY_RETENTION_DAYS", defaultHistoryRetentionDays)) * 24 * time.Hour,
		RateLimitFallback:     time.Duration(envInt("RATE_LIMIT_FALLBACK_SECONDS", defaultRateLimitFallbackSec)) * time.Second,
		QuotaFallback:         time.Duration(envInt("QUOTA_FALLBACK_BLOCK_SECONDS", defaultQuotaFallbackSec)) * time.Second,
		LogLevel:              slogLevel(strings.ToLower(envOr("LOG_LEVEL", "info"))),
		UserAgentTemplate:     envOr("USER_AGENT_TEMPLATE", "Codex Desktop/26.409.61251 ({platform}; {arch})"),
		ChromiumVersion:       envOr("CHROMIUM_VERSION", "144"),
		Platform:              envOr("CLIENT_PLATFORM", "win32"),
		ClientHintPlatform:    envOr("CLIENT_HINT_PLATFORM", "Windows"),
		Arch:                  envOr("CLIENT_ARCH", "x64"),
		DefaultAcceptLanguage: envOr("DEFAULT_ACCEPT_LANGUAGE", "en-US,en;q=0.9"),
		HeaderOrder: []string{
			"authorization",
			"chatgpt-account-id",
			"originator",
			"x-openai-internal-codex-residency",
			"x-client-request-id",
			"x-codex-turn-state",
			"openai-beta",
			"user-agent",
			"sec-ch-ua",
			"sec-ch-ua-mobile",
			"sec-ch-ua-platform",
			"accept-encoding",
			"accept-language",
			"sec-fetch-site",
			"sec-fetch-mode",
			"sec-fetch-dest",
			"content-type",
			"accept",
			"cookie",
		},
	}

	if cfg.ProxyAPIKey == "" {
		return Config{}, fmt.Errorf("PROXY_API_KEY must be set")
	}
	if raw := strings.TrimSpace(os.Getenv("USAGE_SNAPSHOT_INTERVAL_MINUTES")); raw != "" && cfg.UsageSnapshotInterval <= 0 {
		return Config{}, fmt.Errorf("USAGE_SNAPSHOT_INTERVAL_MINUTES must be a positive integer")
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create data dir: %w", err)
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
