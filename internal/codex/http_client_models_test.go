package codex

import (
	"testing"

	"chatgpt-codex-proxy/internal/config"
)

func TestParseCodexModelsResponseAcceptsLiveShape(t *testing.T) {
	t.Parallel()

	payload := `{
		"models": [
			{
				"slug": "gpt-5.4",
				"display_name": "gpt-5.4",
				"description": "Flagship",
				"default_reasoning_level": "medium",
				"supported_reasoning_levels": [
					{"effort": "low", "description": "Fastest"},
					{"effort": "medium", "description": "Balanced"}
				]
			},
			{
				"slug": "gpt-5.4-mini",
				"display_name": "GPT-5.4-Mini",
				"default_reasoning_level": "medium"
			}
		]
	}`

	models, err := parseCodexModelsResponse(payload)
	if err != nil {
		t.Fatalf("parseCodexModelsResponse() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[0].Slug != "gpt-5.4" {
		t.Fatalf("models[0].Slug = %q, want gpt-5.4", models[0].Slug)
	}
	if models[0].DisplayName != "gpt-5.4" {
		t.Fatalf("models[0].DisplayName = %q, want gpt-5.4", models[0].DisplayName)
	}
	if models[0].DefaultReasoningLevel != "medium" {
		t.Fatalf("models[0].DefaultReasoningLevel = %q, want medium", models[0].DefaultReasoningLevel)
	}
}

func TestParseCodexModelsResponseRejectsUnsupportedShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "bare array",
			payload: `[{"slug":"gpt-5.4"}]`,
		},
		{
			name:    "data field",
			payload: `{"data":[{"slug":"gpt-5.4"}]}`,
		},
		{
			name:    "chat_models field",
			payload: `{"chat_models":{"models":[{"slug":"gpt-5.4"}]}}`,
		},
		{
			name:    "categories field",
			payload: `{"categories":[{"models":[{"slug":"gpt-5.4"}]}]}`,
		},
		{
			name:    "missing models",
			payload: `{}`,
		},
		{
			name:    "empty models",
			payload: `{"models":[]}`,
		},
		{
			name:    "nested models tree",
			payload: `{"models":[{"models":[{"slug":"gpt-5.4"}]}]}`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			models, err := parseCodexModelsResponse(tc.payload)
			if err == nil {
				t.Fatalf("parseCodexModelsResponse() models = %#v, want error", models)
			}
		})
	}
}

func TestCodexModelsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "includes client version",
			cfg: config.Config{
				CodexBaseURL:  "https://chatgpt.com/backend-api",
				ClientVersion: "26.409.61251",
			},
			want: "https://chatgpt.com/backend-api/codex/models?client_version=26.409.61251",
		},
		{
			name: "omits client version when blank",
			cfg: config.Config{
				CodexBaseURL: "https://chatgpt.com/backend-api",
			},
			want: "https://chatgpt.com/backend-api/codex/models",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := NewHTTPClient(tc.cfg)
			if got := client.codexModelsURL(); got != tc.want {
				t.Fatalf("codexModelsURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
