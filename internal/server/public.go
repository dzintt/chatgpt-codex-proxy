package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Headers() http.Header
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

	account, stream, quota, err := a.openHTTPStream(c.Request.Context(), normalized, "", "")
	if err != nil {
		status, code, message := a.classifyUpstreamError(account.ID, err)
		a.logUpstreamRequestFailure(c, "chat_completions", account.ID, status, code, err)
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
		a.logUpstreamStreamFailure(c, "chat_completions", account.ID, "", err)
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
	turnState := ""
	if normalized.PreviousResponseID != "" {
		record, ok := a.continuations.Get(normalized.PreviousResponseID)
		if !ok {
			a.writeOpenAIError(c, http.StatusBadRequest, "invalid_previous_response_id", "unknown or expired previous_response_id", "invalid_request_error")
			return
		}
		preferredID = record.AccountID
		turnState = record.TurnState
		if history := deserializeContinuationInput(record.InputHistory); len(history) > 0 {
			normalized.Input = append(history, normalized.Input...)
			normalized.PreviousResponseID = ""
		}
	}
	account, stream, quota, err := a.openStream(c.Request.Context(), normalized, preferredID, turnState)
	if err != nil {
		accountID := firstString(account.ID, preferredID)
		status, code, message := a.classifyUpstreamError(accountID, err)
		a.logUpstreamRequestFailure(c, "responses", accountID, status, code, err)
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
		a.logUpstreamStreamFailure(c, "responses", account.ID, "", err)
		a.writeOpenAIError(c, status, code, message, "api_error")
		return
	}
	c.JSON(http.StatusOK, accumulator.ResponsesObject())
}

func (a *App) openStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	if normalized.Endpoint == translate.EndpointResponses && normalized.PreviousResponseID != "" {
		return a.openWSStream(ctx, normalized, preferredID, turnState)
	}
	return a.openHTTPStream(ctx, normalized, preferredID, turnState)
}

func (a *App) openHTTPStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.accountMgr.AcquireReady(ctx, preferredID)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	request := normalized.ToCodexRequest()
	stream, err := a.httpClient.StreamResponse(ctx, account, request, turnState)
	if err != nil {
		return account, nil, nil, err
	}
	return account, stream, codex.ParseQuotaFromHeaders(stream.Headers()), nil
}

func (a *App) openWSStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.accountMgr.AcquireReady(ctx, preferredID)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	headers := codex.BuildHeaders(a.cfg, account.Token.AccessToken, codex.HeaderOptions{
		AccountID:   account.AccountID,
		Cookies:     account.Cookies,
		TurnState:   turnState,
		RequestID:   codex.NewRequestID(),
		IncludeBeta: true,
	})
	body := normalized.ToCodexWSCreatePayload()
	endpoint := websocketEndpoint(a.cfg.CodexBaseURL)
	stream, err := a.wsClient.Connect(ctx, endpoint, headers, body)
	if err != nil {
		return account, nil, nil, err
	}
	return account, stream, codex.ParseQuotaFromHeaders(stream.Headers()), nil
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
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			return nil, upstreamErr
		}
		accumulator.Apply(event)
	}

	if accumulator.Usage != nil {
		_ = a.accounts.RecordUsage(account.ID, accumulator.Usage.InputTokens, accumulator.Usage.OutputTokens)
	}
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))
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
			a.logUpstreamStreamFailure(c, "chat_completions", account.ID, accumulator.ResponseID, err)
			writeSSE(c.Writer, "", translate.MustJSON(gin.H{"error": err.Error()}))
			c.Writer.Flush()
			return
		}
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			a.logUpstreamStreamFailure(c, "chat_completions", account.ID, accumulator.ResponseID, upstreamErr)
			writeSSE(c.Writer, "", translate.MustJSON(gin.H{"error": upstreamErr.Error()}))
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
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))

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
			a.logUpstreamStreamFailure(c, "responses", account.ID, accumulator.ResponseID, err)
			writeSSE(c.Writer, "error", translate.MustJSON(gin.H{"error": err.Error()}))
			c.Writer.Flush()
			return
		}
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			a.logUpstreamStreamFailure(c, "responses", account.ID, accumulator.ResponseID, upstreamErr)
			writeSSE(c.Writer, "error", translate.MustJSON(gin.H{"error": upstreamErr.Error()}))
			c.Writer.Flush()
			return
		}
		accumulator.Apply(event)
		payload := responseStreamPayload(event, accumulator)
		writeSSE(c.Writer, event.Type, translate.ResponseEventJSON(event.Type, accumulator.ResponseID, payload))
		c.Writer.Flush()
	}

	if accumulator.Usage != nil {
		_ = a.accounts.RecordUsage(account.ID, accumulator.Usage.InputTokens, accumulator.Usage.OutputTokens)
	}
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))
	writeSSE(c.Writer, "done", []byte("[DONE]"))
	c.Writer.Flush()
}

func responseStreamPayload(event *codex.StreamEvent, accumulator *translate.Accumulator) map[string]any {
	if event == nil || event.Raw == nil {
		return nil
	}
	if event.Type != "response.completed" {
		return event.Raw
	}

	payload := cloneAnyMap(event.Raw)
	response, _ := payload["response"].(map[string]any)
	if response == nil {
		return payload
	}

	text := accumulator.Text()
	if output, ok := response["output"].([]any); !ok || len(output) == 0 {
		response["output"] = []map[string]any{{
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{{
				"type": "output_text",
				"text": text,
			}},
		}}
	}
	if strings.TrimSpace(serverStringValue(response["output_text"])) == "" && strings.TrimSpace(text) != "" {
		response["output_text"] = text
	}
	if strings.TrimSpace(serverStringValue(response["status"])) == "" {
		response["status"] = "completed"
	}
	if accumulator.Usage != nil {
		response["usage"] = map[string]any{
			"input_tokens":  accumulator.Usage.InputTokens,
			"output_tokens": accumulator.Usage.OutputTokens,
			"total_tokens":  accumulator.Usage.InputTokens + accumulator.Usage.OutputTokens,
		}
	}
	payload["response"] = response
	return payload
}

func (a *App) rememberContinuation(accountID string, accumulator *translate.Accumulator, turnState string) {
	if accumulator == nil || accumulator.ResponseID == "" || accumulator.Normalized.Endpoint != translate.EndpointResponses {
		return
	}
	a.continuations.Put(accounts.ContinuationRecord{
		ResponseID:   accumulator.ResponseID,
		AccountID:    accountID,
		UpstreamID:   accumulator.ResponseID,
		TurnState:    strings.TrimSpace(turnState),
		Model:        firstString(accumulator.Model, accumulator.Normalized.Model),
		InputHistory: continuationInputHistory(accumulator),
		ExpiresAt:    timeNowUTC().Add(a.cfg.ContinuationTTL),
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

func upstreamEventError(event *codex.StreamEvent) error {
	if event == nil {
		return nil
	}
	if event.Type != "error" && event.Type != "response.failed" {
		return nil
	}
	if nested := serverNestedMap(event.Raw, "error"); nested != nil {
		if message := firstString(serverStringValue(nested["message"]), serverStringValue(nested["detail"])); message != "" {
			return errors.New(message)
		}
	}
	if message := firstString(serverStringValue(event.Raw["message"]), serverStringValue(event.Raw["detail"])); message != "" {
		return errors.New(message)
	}
	return fmt.Errorf("upstream %s", event.Type)
}

func serverNestedMap(raw map[string]any, key string) map[string]any {
	if raw == nil {
		return nil
	}
	value, _ := raw[key].(map[string]any)
	return value
}

func serverStringValue(value any) string {
	str, _ := value.(string)
	return str
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if mapped, ok := value.(map[string]any); ok {
			dst[key] = cloneAnyMap(mapped)
			continue
		}
		dst[key] = value
	}
	return dst
}

func continuationInputHistory(accumulator *translate.Accumulator) []map[string]any {
	history := serializeContinuationInput(accumulator.Normalized.Input)
	if assistant := continuationAssistantTurn(accumulator); assistant != nil {
		history = append(history, assistant)
	}
	return history
}

func continuationAssistantTurn(accumulator *translate.Accumulator) map[string]any {
	text := strings.TrimSpace(accumulator.Text())
	if text == "" {
		return nil
	}
	item := codex.InputItem{
		Role: "assistant",
		Content: []codex.ContentPart{{
			Type: "output_text",
			Text: text,
		}},
	}
	serialized := serializeContinuationInput([]codex.InputItem{item})
	if len(serialized) == 0 {
		return nil
	}
	return serialized[0]
}

func serializeContinuationInput(items []codex.InputItem) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return nil
	}
	var cloned []map[string]any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil
	}
	return cloned
}

func deserializeContinuationInput(items []map[string]any) []codex.InputItem {
	if len(items) == 0 {
		return nil
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return nil
	}
	var decoded []codex.InputItem
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil
	}
	return decoded
}
