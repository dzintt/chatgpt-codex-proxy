package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

var errIncompleteResponse = errors.New("upstream stream ended before response.completed")

func (a *App) handleChatCompletions(c *gin.Context) {
	var req openai.ChatCompletionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}

	normalized, err := translate.ChatCompletions(req, a.cfg.DefaultModel)
	if err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}
	a.logCompatibilityWarnings(c, "chat_completions", normalized.CompatibilityWarnings)

	account, stream, quota, err := a.openHTTPStream(c.Request.Context(), normalized, "", "")
	if err != nil {
		a.handleOpenStreamError(c, "chat_completions", account.ID, account.ID, err)
		return
	}
	defer stream.Close()
	a.observeQuotaSnapshot(account.ID, quota)

	if normalized.Stream {
		a.streamChatCompletion(c, account, normalized, stream)
		return
	}

	accumulator, err := a.collectEvents(account, normalized, stream)
	if err != nil {
		a.respondOpenAIUpstreamStreamError(c, "chat_completions", account.ID, "", err)
		return
	}
	response := accumulator.ChatCompletionObject()
	if err := translate.PatchChatCompletionObjectForTuple(response, normalized.TupleSchema); err != nil {
		a.logTupleReconversionWarning(c, "chat_completions", accumulator.ResponseID, err)
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) handleResponses(c *gin.Context) {
	var req openai.ResponsesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}

	normalized, err := translate.Responses(req, a.cfg.DefaultModel)
	if err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}
	a.logCompatibilityWarnings(c, "responses", normalized.CompatibilityWarnings)

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
		reportedAccountID := firstString(account.ID, preferredID)
		a.handleOpenStreamError(c, "responses", account.ID, reportedAccountID, err)
		return
	}
	defer stream.Close()
	a.observeQuotaSnapshot(account.ID, quota)

	if normalized.Stream {
		a.streamResponses(c, account, normalized, stream)
		return
	}

	accumulator, err := a.collectEvents(account, normalized, stream)
	if err != nil {
		a.respondOpenAIUpstreamStreamError(c, "responses", account.ID, "", err)
		return
	}
	response := accumulator.ResponsesObject()
	if err := translate.PatchResponsesObjectForTuple(response, normalized.TupleSchema); err != nil {
		a.logTupleReconversionWarning(c, "responses", accumulator.ResponseID, err)
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) openStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	if normalized.Endpoint == translate.EndpointResponses && normalized.PreviousResponseID != "" {
		return a.openWSStream(ctx, normalized, preferredID, turnState)
	}
	return a.openHTTPStream(ctx, normalized, preferredID, turnState)
}

func (a *App) openHTTPStream(ctx context.Context, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.acquireReadyAccount(ctx, preferredID)
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
	account, err := a.acquireReadyAccount(ctx, preferredID)
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
		if a.observeQuotaEvent(account, event) {
			continue
		}
		accumulator.Apply(event)
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			return nil, upstreamErr
		}
		if event.Type == "response.completed" {
			break
		}
	}

	if accumulator.ResponseID == "" || !accumulator.IsCompleted() {
		return nil, errIncompleteResponse
	}

	a.accounts.NoteSuccess(account.ID)
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))
	return accumulator, nil
}

func (a *App) streamChatCompletion(c *gin.Context, account accounts.Record, normalized translate.NormalizedRequest, stream eventStream) {
	prepareStreamResponse(c)

	accumulator := translate.NewAccumulator(normalized)
	toolCallIndex := make(map[string]int)
	toolCallSawDelta := make(map[string]bool)
	nextToolCallIndex := 0
	var tupleTextBuffer strings.Builder
	writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk("", normalized.Model, map[string]any{"role": "assistant"}, "")))
	c.Writer.Flush()

	for {
		event, err := stream.NextEvent()
		if err != nil {
			if err == io.EOF {
				if !accumulator.IsCompleted() {
					a.respondStreamError(c, "chat_completions", account.ID, accumulator.ResponseID, "", errIncompleteResponse)
					return
				}
				break
			}
			a.respondStreamError(c, "chat_completions", account.ID, accumulator.ResponseID, "", err)
			return
		}
		if a.observeQuotaEvent(account, event) {
			continue
		}
		accumulator.Apply(event)
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			a.respondClassifiedStreamError(c, "chat_completions", account.ID, accumulator.ResponseID, "", upstreamErr)
			return
		}
		if event.Type == "response.completed" {
		}
		if emitted := streamChatToolCallChunk(c.Writer, accumulator, normalized, event, toolCallIndex, toolCallSawDelta, &nextToolCallIndex); emitted {
			c.Writer.Flush()
			continue
		}
		switch event.Type {
		case "response.output_text.delta":
			delta := serverStringValue(event.Raw["delta"])
			if delta == "" {
				continue
			}
			if normalized.TupleSchema != nil {
				tupleTextBuffer.WriteString(delta)
				continue
			}
			writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{"content": delta}, "")))
			c.Writer.Flush()
		case "response.output_text.done":
			if normalized.TupleSchema != nil {
				if text := serverStringValue(event.Raw["text"]); text != "" {
					tupleTextBuffer.Reset()
					tupleTextBuffer.WriteString(text)
				}
			}
		case "response.completed":
			if normalized.TupleSchema != nil && strings.TrimSpace(tupleTextBuffer.String()) != "" {
				reconverted := tupleTextBuffer.String()
				if patched, err := translate.ReconvertJSONText(reconverted, normalized.TupleSchema); err != nil {
					a.logTupleReconversionWarning(c, "chat_completions", accumulator.ResponseID, err)
				} else {
					reconverted = patched
				}
				writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{"content": reconverted}, "")))
				c.Writer.Flush()
			}
		}
		if event.Type == "response.completed" {
			break
		}
	}

	a.accounts.NoteSuccess(account.ID)
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))

	writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{}, chatStreamFinishReason(accumulator))))
	_, _ = io.WriteString(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()
}

func (a *App) streamResponses(c *gin.Context, account accounts.Record, normalized translate.NormalizedRequest, stream eventStream) {
	prepareStreamResponse(c)

	accumulator := translate.NewAccumulator(normalized)
	var tupleTextBuffer strings.Builder
	for {
		event, err := stream.NextEvent()
		if err != nil {
			if err == io.EOF {
				if !accumulator.IsCompleted() {
					a.respondStreamError(c, "responses", account.ID, accumulator.ResponseID, "error", errIncompleteResponse)
					return
				}
				break
			}
			a.respondStreamError(c, "responses", account.ID, accumulator.ResponseID, "error", err)
			return
		}
		if a.observeQuotaEvent(account, event) {
			continue
		}
		accumulator.Apply(event)
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			a.respondClassifiedStreamError(c, "responses", account.ID, accumulator.ResponseID, "error", upstreamErr)
			return
		}
		if event.Type == "response.completed" {
		}
		if normalized.TupleSchema != nil {
			switch event.Type {
			case "response.output_text.delta":
				tupleTextBuffer.WriteString(serverStringValue(event.Raw["delta"]))
				continue
			case "response.output_text.done":
				if text := serverStringValue(event.Raw["text"]); text != "" {
					tupleTextBuffer.Reset()
					tupleTextBuffer.WriteString(text)
				}
				continue
			case "response.completed":
				if strings.TrimSpace(tupleTextBuffer.String()) != "" {
					reconverted := tupleTextBuffer.String()
					if patched, err := translate.ReconvertJSONText(reconverted, normalized.TupleSchema); err != nil {
						a.logTupleReconversionWarning(c, "responses", accumulator.ResponseID, err)
					} else {
						reconverted = patched
					}
					writeSSE(c.Writer, "response.output_text.delta", translate.ResponseEventJSON("response.output_text.delta", accumulator.ResponseID, map[string]any{
						"delta": reconverted,
					}))
					c.Writer.Flush()
				}
			}
		}
		payload := responseStreamPayload(event, accumulator)
		if normalized.TupleSchema != nil && event.Type == "response.completed" {
			if err := translate.PatchResponseCompletedPayloadForTuple(payload, normalized.TupleSchema); err != nil {
				a.logTupleReconversionWarning(c, "responses", accumulator.ResponseID, err)
			}
		}
		writeSSE(c.Writer, event.Type, translate.ResponseEventJSON(event.Type, accumulator.ResponseID, payload))
		c.Writer.Flush()
		if event.Type == "response.completed" {
			break
		}
	}

	a.accounts.NoteSuccess(account.ID)
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))
	writeSSE(c.Writer, "done", []byte("[DONE]"))
	c.Writer.Flush()
}

func streamChatToolCallChunk(w io.Writer, accumulator *translate.Accumulator, normalized translate.NormalizedRequest, event *codex.StreamEvent, toolCallIndex map[string]int, toolCallSawDelta map[string]bool, nextToolCallIndex *int) bool {
	if event == nil || !strings.HasPrefix(event.Type, "response.function_call_arguments.") {
		return false
	}

	callID := firstString(serverStringValue(event.Raw["call_id"]), serverStringValue(event.Raw["item_id"]))
	if callID == "" {
		return false
	}

	idx, exists := toolCallIndex[callID]
	if !exists {
		idx = *nextToolCallIndex
		toolCallIndex[callID] = idx
		*nextToolCallIndex = *nextToolCallIndex + 1
		writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{
			"tool_calls": []map[string]any{{
				"index": idx,
				"id":    callID,
				"type":  "function",
				"function": map[string]any{
					"name":      serverStringValue(event.Raw["name"]),
					"arguments": "",
				},
			}},
		}, "")))
	}

	switch event.Type {
	case "response.function_call_arguments.delta":
		delta := serverStringValue(event.Raw["delta"])
		if delta == "" {
			return false
		}
		toolCallSawDelta[callID] = true
		writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{
			"tool_calls": []map[string]any{{
				"index": idx,
				"function": map[string]any{
					"arguments": delta,
				},
			}},
		}, "")))
		return true
	case "response.function_call_arguments.done":
		if toolCallSawDelta[callID] {
			return false
		}
		arguments := serverStringValue(event.Raw["arguments"])
		writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, firstString(accumulator.Model, normalized.Model), map[string]any{
			"tool_calls": []map[string]any{{
				"index": idx,
				"function": map[string]any{
					"arguments": arguments,
				},
			}},
		}, "")))
		return true
	default:
		return false
	}
}

func chatStreamFinishReason(accumulator *translate.Accumulator) string {
	if accumulator != nil && len(accumulator.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
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
		response["usage"] = translate.UsageSummary{
			InputTokens:  accumulator.Usage.InputTokens,
			OutputTokens: accumulator.Usage.OutputTokens,
			TotalTokens:  accumulator.Usage.InputTokens + accumulator.Usage.OutputTokens,
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
	details := extractUpstreamEventDetails(event)
	if details == nil {
		return fmt.Errorf("upstream %s", event.Type)
	}
	return details
}

func extractUpstreamEventDetails(event *codex.StreamEvent) *codex.UpstreamError {
	if event == nil || event.Raw == nil {
		return nil
	}

	nested := firstNestedMap(
		serverNestedMap(event.Raw, "error"),
		serverNestedPathMap(event.Raw, "response", "error"),
	)
	message := firstString(
		serverStringValueFromMap(nested, "message"),
		serverStringValueFromMap(nested, "detail"),
		serverStringValue(event.Raw["message"]),
		serverStringValue(event.Raw["detail"]),
	)
	if message == "" {
		message = fmt.Sprintf("upstream %s", event.Type)
	}

	code := firstString(
		serverStringValueFromMap(nested, "code"),
		serverStringValueFromMap(nested, "type"),
		serverStringValue(event.Raw["code"]),
		serverStringValue(event.Raw["type"]),
	)
	statusCode, ok := serverIntValueFromMap(nested, "status_code")
	if !ok {
		statusCode, ok = serverIntValueFromMap(nested, "status")
	}
	if !ok {
		statusCode, ok = serverIntValue(event.Raw["status_code"])
	}
	if !ok {
		statusCode, ok = serverIntValue(event.Raw["status"])
	}
	if statusCode == 0 {
		statusCode = upstreamStatusCodeFromCode(code)
	}

	return &codex.UpstreamError{
		Op:         "codex stream",
		StatusCode: statusCode,
		Body:       message,
		Code:       code,
		RetryAfter: firstRetryAfterSeconds(nested, event.Raw),
	}
}

func upstreamStatusCodeFromCode(code string) int {
	switch normalized := strings.ToLower(strings.TrimSpace(code)); normalized {
	case "rate_limited", "rate_limit_exceeded", "too_many_requests":
		return http.StatusTooManyRequests
	case "quota_exhausted", "usage_limit_reached", "payment_required", "subscription_required":
		return http.StatusPaymentRequired
	case "invalid_api_key", "unauthorized", "authentication_error", "invalid_token":
		return http.StatusUnauthorized
	default:
		switch {
		case strings.Contains(normalized, "rate_limit"), strings.Contains(normalized, "too_many"):
			return http.StatusTooManyRequests
		case strings.Contains(normalized, "quota"), strings.Contains(normalized, "usage_limit"), strings.Contains(normalized, "payment"):
			return http.StatusPaymentRequired
		case strings.Contains(normalized, "unauthorized"), strings.Contains(normalized, "auth"):
			return http.StatusUnauthorized
		default:
			return 0
		}
	}
}

func firstRetryAfterSeconds(values ...map[string]any) int {
	now := timeNowUTC()
	for _, value := range values {
		if value == nil {
			continue
		}
		if seconds, ok := serverIntValue(value["resets_in_seconds"]); ok && seconds > 0 {
			return seconds
		}
		if resetAt, ok := serverIntValue(value["resets_at"]); ok && resetAt > 0 {
			diff := resetAt - int(now.Unix())
			if diff > 0 {
				return diff
			}
		}
	}
	return 0
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

func serverNestedPathMap(raw map[string]any, keys ...string) map[string]any {
	current := raw
	for idx, key := range keys {
		if current == nil {
			return nil
		}
		value, _ := current[key].(map[string]any)
		if idx == len(keys)-1 {
			return value
		}
		current = value
	}
	return nil
}

func serverStringValueFromMap(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	return serverStringValue(raw[key])
}

func serverIntValueFromMap(raw map[string]any, key string) (int, bool) {
	if raw == nil {
		return 0, false
	}
	return serverIntValue(raw[key])
}

func serverIntValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func firstNestedMap(values ...map[string]any) map[string]any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
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

func prepareStreamResponse(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Status(http.StatusOK)
}

func (a *App) observeQuotaSnapshot(accountID string, quota *accounts.QuotaSnapshot) {
	if quota == nil || strings.TrimSpace(accountID) == "" {
		return
	}
	if err := a.accounts.ObserveQuota(accountID, quota); err != nil {
		a.logger.Warn("persist quota snapshot failed", "account_id", accountID, "error", err.Error())
	}
}

func (a *App) observeQuotaEvent(account accounts.Record, event *codex.StreamEvent) bool {
	if event == nil || event.Type != "codex.rate_limits" {
		return false
	}
	quota := codex.ParseQuotaFromEvent(event, firstString(account.PlanType, "unknown"))
	a.observeQuotaSnapshot(account.ID, quota)
	return true
}

func (a *App) respondOpenAIInvalidRequest(c *gin.Context, err error) {
	a.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_request_error")
}

func (a *App) respondOpenAIUpstreamRequestError(c *gin.Context, endpoint, accountID string, err error) {
	status, code, message := a.classifyUpstreamError(accountID, err)
	a.logUpstreamRequestFailure(c, endpoint, accountID, status, code, err)
	a.writeOpenAIError(c, status, code, message, "api_error")
}

func (a *App) handleOpenStreamError(c *gin.Context, endpoint, actualAccountID, reportedAccountID string, err error) {
	status, code, message := a.classifyUpstreamError(strings.TrimSpace(actualAccountID), err)
	logAccountID := firstString(actualAccountID, reportedAccountID)
	a.logUpstreamRequestFailure(c, endpoint, logAccountID, status, code, err)
	a.writeOpenAIError(c, status, code, message, "api_error")
}

func (a *App) respondOpenAIUpstreamStreamError(c *gin.Context, endpoint, accountID, responseID string, err error) {
	status, code, message := a.classifyUpstreamError(accountID, err)
	a.logUpstreamStreamFailure(c, endpoint, accountID, responseID, err)
	a.writeOpenAIError(c, status, code, message, "api_error")
}

func (a *App) respondClassifiedStreamError(c *gin.Context, endpoint, accountID, responseID, eventName string, err error) {
	_, _, message := a.classifyUpstreamError(accountID, err)
	a.logUpstreamStreamFailure(c, endpoint, accountID, responseID, err)
	writeSSE(c.Writer, eventName, translate.MustJSON(gin.H{"error": message}))
	c.Writer.Flush()
}

func (a *App) respondStreamError(c *gin.Context, endpoint, accountID, responseID, eventName string, err error) {
	a.logUpstreamStreamFailure(c, endpoint, accountID, responseID, err)
	writeSSE(c.Writer, eventName, translate.MustJSON(gin.H{"error": err.Error()}))
	c.Writer.Flush()
}

func (a *App) acquireReadyAccount(ctx context.Context, preferredID string) (accounts.Record, error) {
	return a.accountMgr.AcquireReady(ctx, preferredID)
}
