//go:build live

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/models"
)

func TestLiveCodexModelsEndpoint(t *testing.T) {
	t.Parallel()

	accessToken := os.Getenv("CODEX_ACCESS_TOKEN")
	accountID := os.Getenv("CODEX_ACCOUNT_ID")
	if accessToken == "" || accountID == "" {
		t.Skip("CODEX_ACCESS_TOKEN and CODEX_ACCOUNT_ID are required for live codex models test")
	}

	baseURL := os.Getenv("CODEX_BASE_URL")
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api"
	}
	clientVersion := os.Getenv("CODEX_CLIENT_VERSION")
	if clientVersion == "" {
		clientVersion = "26.409.61251"
	}

	cfg := config.Config{
		CodexBaseURL:          baseURL,
		ClientVersion:         clientVersion,
		RequestTimeout:        120 * time.Second,
		Originator:            "Codex Desktop",
		Residency:             "us",
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

	record := accounts.Record{
		ID:        "acct_live_models",
		AccountID: accountID,
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: accessToken,
			ExpiresAt:   time.Now().UTC().Add(time.Hour),
		},
	}

	client := codex.NewHTTPClient(cfg)
	defer client.Close()

	rawModels, err := client.GetCodexModels(context.Background(), record)
	if err != nil {
		t.Fatalf("GetCodexModels() error = %v", err)
	}
	if len(rawModels) == 0 {
		t.Fatal("GetCodexModels() returned no models")
	}

	normalized := models.NormalizeBackendEntries(rawModels)
	if len(normalized) == 0 {
		t.Fatal("NormalizeBackendEntries() returned no models")
	}
	for _, model := range normalized {
		if model.ID == "" {
			t.Fatalf("normalized model has empty ID: %#v", model)
		}
	}
}
