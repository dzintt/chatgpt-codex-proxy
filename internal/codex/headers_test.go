package codex

import (
	"testing"

	"chatgpt-codex-proxy/internal/config"
)

func TestBuildHeadersUsesConfiguredDesktopIdentity(t *testing.T) {
	cfg := config.Config{
		Originator:            "Codex Desktop",
		Residency:             "us",
		OpenAIBeta:            "responses_websockets=2026-02-06",
		UserAgentTemplate:     "Codex Desktop/26.409.61251 ({platform}; {arch})",
		ChromiumVersion:       "144",
		Platform:              "win32",
		ClientHintPlatform:    "Windows",
		Arch:                  "x64",
		DefaultAcceptLanguage: "en-US,en;q=0.9",
	}

	headers := BuildHeaders(cfg, "token", HeaderOptions{IncludeBeta: true})

	if got := headers.Get("User-Agent"); got != "Codex Desktop/26.409.61251 (win32; x64)" {
		t.Fatalf("unexpected user-agent: %q", got)
	}
	if got := headers.Get("sec-ch-ua-platform"); got != `"Windows"` {
		t.Fatalf("unexpected client hint platform: %q", got)
	}
}

func TestOAuthDefaultHeadersReuseDesktopIdentity(t *testing.T) {
	svc := OAuthService{
		cfg: config.Config{
			UserAgentTemplate: "Codex Desktop/26.409.61251 ({platform}; {arch})",
			Platform:          "win32",
			Arch:              "x64",
		},
	}

	headers := svc.defaultHeaders()

	if got := headers.Get("User-Agent"); got != "Codex Desktop/26.409.61251 (win32; x64)" {
		t.Fatalf("unexpected oauth user-agent: %q", got)
	}
}
