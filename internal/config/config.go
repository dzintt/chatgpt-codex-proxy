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
	defaultListenPort            = 8080
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
	defaultContinuationTTLMinute = 60
	defaultRequestTimeoutSecond  = 120
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
	ContinuationTTL       time.Duration
	RequestTimeout        time.Duration
	RefreshSkew           time.Duration
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

	dataDir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	if !filepath.IsAbs(dataDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd: %w", err)
		}
		dataDir = filepath.Join(cwd, dataDir)
	}

	listenAddr, err := loadListenAddr()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:            listenAddr,
		DataDir:               dataDir,
		ProxyAPIKey:           strings.TrimSpace(os.Getenv("PROXY_API_KEY")),
		DefaultModel:          defaultDefaultModel,
		Originator:            defaultOriginator,
		OpenAIBeta:            defaultOpenAIBeta,
		Residency:             defaultResidency,
		RotationStrategy:      defaultRotationStrategy,
		CodexBaseURL:          defaultCodexBaseURL,
		AuthIssuer:            defaultAuthIssuer,
		OAuthClientID:         defaultClientID,
		LoginTimeout:          time.Duration(defaultLoginTimeoutSeconds) * time.Second,
		ContinuationTTL:       time.Duration(defaultContinuationTTLMinute) * time.Minute,
		RequestTimeout:        time.Duration(defaultRequestTimeoutSecond) * time.Second,
		RefreshSkew:           60 * time.Second,
		LogLevel:              slogLevel("info"),
		UserAgentTemplate:     "Codex Desktop/26.409.61251 ({platform}; {arch})",
		ChromiumVersion:       "147",
		Platform:              "win32",
		ClientHintPlatform:    "Windows",
		Arch:                  "x64",
		DefaultAcceptLanguage: "en-US,en;q=0.9",
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

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create data dir: %w", err)
	}

	return cfg, nil
}

func loadListenAddr() (string, error) {
	raw := strings.TrimSpace(os.Getenv("PORT"))
	if raw == "" {
		return ":" + strconv.Itoa(defaultListenPort), nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("PORT must be a valid TCP port")
	}
	return ":" + strconv.Itoa(port), nil
}
