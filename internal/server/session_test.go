package server

import (
	"context"
	"testing"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/models"
	"chatgpt-codex-proxy/internal/translate"
)

func TestResolveSessionImplicitResumeTrimsHistoryAndSetsContinuationState(t *testing.T) {
	t.Parallel()

	app := &App{
		continuations: accounts.NewContinuationManager(time.Minute),
	}
	normalized := translate.NormalizedRequest{
		Endpoint:     translate.EndpointResponses,
		Model:        "gpt-5.4",
		Instructions: "Be concise.",
		Input: []codex.InputItem{
			userText("hello"),
			assistantText("I will call a tool."),
			{Type: "function_call", CallID: "call_1", Name: "Search", Arguments: `{"q":"hello"}`},
			{Type: "function_call_output", CallID: "call_1", OutputText: "tool result"},
			userText("summarize it"),
		},
	}
	app.continuations.Put(accounts.ContinuationRecord{
		ResponseID:      "resp_1",
		AccountID:       "acct_1",
		ConversationKey: resolutionConversationKey(normalized),
		TurnState:       "turn_1",
		Instructions:    normalized.Instructions,
		Model:           normalized.Model,
		InputHistory:    continuationHistoryPrefix(normalized.Input[:3]),
		FunctionCallIDs: []string{"call_1"},
	})

	resolution, err := app.resolveSession(normalized)
	if err != nil {
		t.Fatalf("resolveSession() error = %v", err)
	}
	if !resolution.ImplicitResume {
		t.Fatal("ImplicitResume = false, want true")
	}
	if resolution.Request.PreviousResponseID != "resp_1" {
		t.Fatalf("PreviousResponseID = %q, want resp_1", resolution.Request.PreviousResponseID)
	}
	if resolution.PreferredAccountID != "acct_1" {
		t.Fatalf("PreferredAccountID = %q, want acct_1", resolution.PreferredAccountID)
	}
	if resolution.TurnState != "turn_1" {
		t.Fatalf("TurnState = %q, want turn_1", resolution.TurnState)
	}
	if resolution.Request.PromptCacheKey == "" {
		t.Fatal("PromptCacheKey = empty, want derived key")
	}
	if len(resolution.Request.Input) != 2 {
		t.Fatalf("len(trimmed input) = %d, want 2", len(resolution.Request.Input))
	}
	if resolution.Request.Input[0].Type != "function_call_output" {
		t.Fatalf("trimmed input[0].Type = %q, want function_call_output", resolution.Request.Input[0].Type)
	}
	if resolution.Request.Input[1].Role != "user" {
		t.Fatalf("trimmed input[1].Role = %q, want user", resolution.Request.Input[1].Role)
	}
}

func TestResolveSessionSkipsImplicitResumeForUnknownToolOutputCallID(t *testing.T) {
	t.Parallel()

	app := &App{
		continuations: accounts.NewContinuationManager(time.Minute),
	}
	normalized := translate.NormalizedRequest{
		Endpoint:     translate.EndpointResponses,
		Model:        "gpt-5.4",
		Instructions: "Be concise.",
		Input: []codex.InputItem{
			userText("hello"),
			{Type: "function_call", CallID: "call_1", Name: "Search", Arguments: `{"q":"hello"}`},
			{Type: "function_call_output", CallID: "call_other", OutputText: "tool result"},
			userText("summarize it"),
		},
	}
	app.continuations.Put(accounts.ContinuationRecord{
		ResponseID:      "resp_1",
		AccountID:       "acct_1",
		ConversationKey: resolutionConversationKey(normalized),
		TurnState:       "turn_1",
		Instructions:    normalized.Instructions,
		Model:           normalized.Model,
		InputHistory:    continuationHistoryPrefix(normalized.Input[:2]),
		FunctionCallIDs: []string{"call_1"},
	})

	resolution, err := app.resolveSession(normalized)
	if err != nil {
		t.Fatalf("resolveSession() error = %v", err)
	}
	if resolution.ImplicitResume {
		t.Fatal("ImplicitResume = true, want false")
	}
	if resolution.Request.PreviousResponseID != "" {
		t.Fatalf("PreviousResponseID = %q, want empty", resolution.Request.PreviousResponseID)
	}
	if len(resolution.Request.Input) != len(normalized.Input) {
		t.Fatalf("len(input) = %d, want %d", len(resolution.Request.Input), len(normalized.Input))
	}
}

func TestResolveSessionChoosesMatchingHistoryWithinConversationBucket(t *testing.T) {
	t.Parallel()

	app := &App{
		continuations: accounts.NewContinuationManager(time.Minute),
	}
	normalized := translate.NormalizedRequest{
		Endpoint:     translate.EndpointResponses,
		Model:        "gpt-5.4",
		Instructions: "Be concise.",
		Input: []codex.InputItem{
			userText("hello"),
			assistantText("assistant one"),
			userText("follow up"),
		},
	}
	conversationKey := resolutionConversationKey(normalized)

	app.continuations.Put(accounts.ContinuationRecord{
		ResponseID:      "resp_other",
		AccountID:       "acct_other",
		ConversationKey: conversationKey,
		TurnState:       "turn_other",
		Instructions:    normalized.Instructions,
		Model:           normalized.Model,
		InputHistory: continuationHistoryPrefix([]codex.InputItem{
			userText("hello"),
			assistantText("different assistant"),
		}),
	})
	app.continuations.Put(accounts.ContinuationRecord{
		ResponseID:      "resp_match",
		AccountID:       "acct_match",
		ConversationKey: conversationKey,
		TurnState:       "turn_match",
		Instructions:    normalized.Instructions,
		Model:           normalized.Model,
		InputHistory: continuationHistoryPrefix([]codex.InputItem{
			userText("hello"),
			assistantText("assistant one"),
		}),
	})

	resolution, err := app.resolveSession(normalized)
	if err != nil {
		t.Fatalf("resolveSession() error = %v", err)
	}
	if !resolution.ImplicitResume {
		t.Fatal("ImplicitResume = false, want true")
	}
	if resolution.Request.PreviousResponseID != "resp_match" {
		t.Fatalf("PreviousResponseID = %q, want resp_match", resolution.Request.PreviousResponseID)
	}
	if resolution.PreferredAccountID != "acct_match" {
		t.Fatalf("PreferredAccountID = %q, want acct_match", resolution.PreferredAccountID)
	}
	if len(resolution.Request.Input) != 1 || resolution.Request.Input[0].Role != "user" {
		t.Fatalf("trimmed input = %#v, want single user follow-up", resolution.Request.Input)
	}
}

func TestAcquireAccountForResolutionOmittedModelUsesRouteScopedDefault(t *testing.T) {
	t.Parallel()

	accountsSvc := newServerAccounts(t, &accounts.Record{
		ID:        "acct_free",
		AccountID: "upstream_free",
		PlanType:  "free",
		Status:    accounts.StatusActive,
		Token: accounts.OAuthToken{
			AccessToken: "token",
			ExpiresAt:   time.Now().UTC().Add(time.Hour),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	catalog := models.NewCatalog(models.BootstrapEntries())
	catalog.ApplyRouteModels("plan:plus", []models.Entry{
		{ID: "gpt-premium-default", Source: models.SourceUpstream, IsDefault: true},
		{ID: "gpt-free-basic", Source: models.SourceUpstream},
	}, time.Now().UTC())
	catalog.ApplyRouteModels("plan:free", []models.Entry{
		{ID: "gpt-free-basic", Source: models.SourceUpstream},
	}, time.Now().UTC())

	app := &App{
		cfg:        config.Config{DefaultModel: "gpt-premium-default"},
		accounts:   accountsSvc,
		accountMgr: codex.NewAccountManager(config.Config{}, accountsSvc, nil, nil, catalog),
		models:     catalog,
	}
	resolution := sessionResolution{
		Request: translate.NormalizedRequest{
			Endpoint:      translate.EndpointResponses,
			ModelExplicit: false,
			Instructions:  "Be concise.",
			Input:         []codex.InputItem{userText("hello")},
		},
		Original: translate.NormalizedRequest{
			Endpoint:      translate.EndpointResponses,
			ModelExplicit: false,
			Instructions:  "Be concise.",
			Input:         []codex.InputItem{userText("hello")},
		},
	}

	account, err := app.acquireAccountForResolution(context.Background(), &resolution)
	if err != nil {
		t.Fatalf("acquireAccountForResolution() error = %v", err)
	}
	if account.ID != "acct_free" {
		t.Fatalf("account.ID = %q, want acct_free", account.ID)
	}
	if resolution.Request.Model != "gpt-free-basic" {
		t.Fatalf("resolved model = %q, want gpt-free-basic", resolution.Request.Model)
	}
	if resolution.Request.PromptCacheKey == "" {
		t.Fatal("PromptCacheKey = empty, want derived after model resolution")
	}
}

func userText(text string) codex.InputItem {
	return codex.InputItem{
		Role: "user",
		Content: []codex.ContentPart{{
			Type: "input_text",
			Text: text,
		}},
	}
}

func assistantText(text string) codex.InputItem {
	return codex.InputItem{
		Role: "assistant",
		Content: []codex.ContentPart{{
			Type: "output_text",
			Text: text,
		}},
	}
}

func continuationHistoryPrefix(items []codex.InputItem) []accounts.ContinuationInputItem {
	history := make([]accounts.ContinuationInputItem, 0, len(items))
	for _, item := range items {
		history = append(history, continuationInputItemFromCodex(item))
	}
	return history
}
