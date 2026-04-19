package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/openai"
	"chatgpt-codex-proxy/internal/translate"
)

type eventStream interface {
	NextEvent() (*codex.StreamEvent, error)
	Close() error
}

func (a *App) handleChatCompletions(c *gin.Context) {
	var req openai.ChatCompletionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_request_error")
		return
	}

	normalized, err := translate.ChatCompletions(req, a.cfg.DefaultModel)
	if err != nil {
		a.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_request_error")
		return
	}

	account, stream, quota, err := a.openHTTPStream(c.Request.Context(), normalized, "")
	if err != nil {
		status, code, message := a.classifyUpstreamError("", err)
		a.writeOpenAIError(c, status, code, message, "api_error")
		return
	}
	defer stream.Close()
	if quota != nil {
		_ = a.accounts.UpdateQuota(account.ID, quota)
	}

	if normalized.Stream {
		a.streamChatCompletion(c, account, normalized, stream)
		return
	}

	accumulator, err := a.collectEvents(account, normalized, stream)
	if err != nil {
		status, code, message := a.classifyUpstreamError(account.ID, err)
		a.writeOpenAIError(c, status, code, message, "api_error")
		return
	}
	c.JSON(http.StatusOK, accumulator.ChatCompletionObject())
}

func (a *App) handleResponses(c *gin.Context) {
	var req openai.ResponsesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_request_error")
		return
	}

	normalized, err := translate.Responses(req, a.cfg.DefaultModel)
	if err != nil {
		a.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_request_error")
		return
	}

	preferredID := ""
	if normalized.PreviousResponseID != "" {
		record, ok := a.continuations.Get(normalized.PreviousResponseID)
		if !ok {
			a.writeOpenAIError(c, http.StatusBadRequest, "invalid_previous_response_id", "unknown or expired previous_response_id", "invalid_request_error")
			return
		}
		preferredID = record.AccountID
	}

	account, stream, quota, err := a.openStream(c.Request.Context(), normalized, preferredID)
	if err != nil {
		status, code, message := a.classifyUpstreamError(preferredID, err)
		a.writeOpenAIError(c, status, code, message, "api_error")
		return
	}
	defer stream.Close()
	if quota != nil {
		_ = a.accounts.UpdateQuota(account.ID, quota)
	}

	if normalized.Stream {
		a.streamResponses(c, account, normalized, stream)
		return
	}

	accumulator, err := a.collectEvents(account, normalized, stream)
	if err != nil {
		status, code, message := a.classifyUpstreamError(account.ID, err)
		a.writeOpenAIError(c, status, code, message, "api_error")
		return
	}
	c.JSON(http.StatusOK, accumulator.ResponsesObject())
}

func (a *App) openStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	if normalized.PreviousResponseID != "" {
		return a.openWSStream(ctx, normalized, preferredID)
	}
	return a.openHTTPStream(ctx, normalized, preferredID)
}

func (a *App) openHTTPStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.accountMgr.AcquireReady(ctx, preferredID)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	stream, err := a.httpClient.StreamResponse(ctx, account, normalized.ToCodexRequest())
	if err != nil {
		return account, nil, nil, err
	}
	return account, stream, codex.ParseQuotaFromHeaders(stream.Headers()), nil
}

func (a *App) openWSStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.accountMgr.AcquireReady(ctx, preferredID)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	headers := codex.BuildHeaders(a.cfg, account.Token.AccessToken, codex.HeaderOptions{
		AccountID:   account.AccountID,
		Cookies:     account.Cookies,
		ContentType: "application/json",
	})
	body := map[string]any{
		"type":     "response.create",
		"response": normalized.ToCodexRequest(),
	}
	endpoint := websocketEndpoint(a.cfg.CodexBaseURL)
	stream, err := a.wsClient.Connect(ctx, endpoint, headers, body)
	if err != nil {
		return account, nil, nil, err
	}
	return account, stream, nil, nil
}

func (a *App) collectEvents(account accounts.Record, normalized translate.NormalizedRequest, stream eventStream) (*translate.Accumulator, error) {
	accumulator := translate.NewAccumulator(normalized)
	for {
		event, err := stream.NextEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		accumulator.Apply(event)
	}

	if accumulator.Usage != nil {
		_ = a.accounts.RecordUsage(account.ID, accumulator.Usage.InputTokens, accumulator.Usage.OutputTokens)
	}
	a.rememberContinuation(account.ID, accumulator)
	return accumulator, nil
}

func (a *App) streamChatCompletion(c *gin.Context, account accounts.Record, normalized translate.NormalizedRequest, stream eventStream) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Status(http.StatusOK)

	accumulator := translate.NewAccumulator(normalized)
	writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk("", normalized.Model, map[string]any{"role": "assistant"}, "")))
	c.Writer.Flush()

	for {
		event, err := stream.NextEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			writeSSE(c.Writer, "", translate.MustJSON(gin.H{"error": err.Error()}))
			c.Writer.Flush()
			return
		}
		before := accumulator.Text()
		accumulator.Apply(event)
		after := accumulator.Text()
		if strings.HasPrefix(after, before) && len(after) > len(before) {
			delta := after[len(before):]
			writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{"content": delta}, "")))
			c.Writer.Flush()
		}
	}

	if accumulator.Usage != nil {
		_ = a.accounts.RecordUsage(account.ID, accumulator.Usage.InputTokens, accumulator.Usage.OutputTokens)
	}
	a.rememberContinuation(account.ID, accumulator)

	writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{}, "stop")))
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

func (a *App) streamResponses(c *gin.Context, account accounts.Record, normalized translate.NormalizedRequest, stream eventStream) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Status(http.StatusOK)

	accumulator := translate.NewAccumulator(normalized)
	for {
		event, err := stream.NextEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			writeSSE(c.Writer, "error", translate.MustJSON(gin.H{"error": err.Error()}))
			c.Writer.Flush()
			return
		}
		accumulator.Apply(event)
		writeSSE(c.Writer, event.Type, translate.ResponseEventJSON(event.Type, accumulator.ResponseID, event.Raw))
		c.Writer.Flush()
	}

	if accumulator.Usage != nil {
		_ = a.accounts.RecordUsage(account.ID, accumulator.Usage.InputTokens, accumulator.Usage.OutputTokens)
	}
	a.rememberContinuation(account.ID, accumulator)
	writeSSE(c.Writer, "done", []byte("[DONE]"))
	c.Writer.Flush()
}

func (a *App) rememberContinuation(accountID string, accumulator *translate.Accumulator) {
	if accumulator == nil || accumulator.ResponseID == "" {
		return
	}
	a.continuations.Put(accounts.ContinuationRecord{
		ResponseID: accumulator.ResponseID,
		AccountID:  accountID,
		UpstreamID: accumulator.ResponseID,
		Model:      firstString(accumulator.Model, accumulator.Normalized.Model),
		ExpiresAt:  timeNowUTC().Add(a.cfg.ContinuationTTL),
	})
}

func websocketEndpoint(baseURL string) string {
	value := strings.TrimRight(baseURL, "/")
	value = strings.Replace(value, "https://", "wss://", 1)
	value = strings.Replace(value, "http://", "ws://", 1)
	return value + "/codex/responses"
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func timeNowUTC() time.Time {
	return time.Now().UTC()
}
