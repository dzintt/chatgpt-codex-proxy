package server

import (
	"bytes"
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
	"chatgpt-codex-proxy/internal/jsonutil"
	"chatgpt-codex-proxy/internal/middleware"
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
	body, err := captureRequestBody(c)
	if err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}
	a.logIncomingPayload(c, "chat_completions", body)

	normalized, err := normalizeChatCompletionsBody(body, a.cfg.DefaultModel)
	if err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}
	a.logCompatibilityWarnings(c, "chat_completions", normalized.CompatibilityWarnings)

	account, stream, quota, err := a.openHTTPStream(c, c.Request.Context(), "chat_completions", normalized, "", "")
	if err != nil {
		a.setRequestAccount(c, account)
		a.handleOpenStreamError(c, "chat_completions", account.ID, account.ID, err)
		return
	}
	a.setRequestAccount(c, account)
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
	body, err := captureRequestBody(c)
	if err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}
	a.logIncomingPayload(c, "responses", body)

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
		if history := continuationInputItemsToCodex(record.InputHistory); len(history) > 0 {
			normalized.Input = append(history, normalized.Input...)
			normalized.PreviousResponseID = ""
		}
	}
	account, stream, quota, err := a.openStream(c, c.Request.Context(), "responses", normalized, preferredID, turnState)
	if err != nil {
		a.setRequestAccount(c, account)
		reportedAccountID := jsonutil.FirstNonEmpty(account.ID, preferredID)
		a.handleOpenStreamError(c, "responses", account.ID, reportedAccountID, err)
		return
	}
	a.setRequestAccount(c, account)
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

func (a *App) openStream(c *gin.Context, ctx context.Context, endpoint string, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	if normalized.Endpoint == translate.EndpointResponses && normalized.PreviousResponseID != "" {
		return a.openWSStream(c, ctx, endpoint, normalized, preferredID, turnState)
	}
	return a.openHTTPStream(c, ctx, endpoint, normalized, preferredID, turnState)
}

func (a *App) openHTTPStream(c *gin.Context, ctx context.Context, endpoint string, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.acquireReadyAccount(ctx, preferredID)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	request := normalized.ToCodexRequest()
	a.logUpstreamPayload(c, endpoint, "http", account.ID, codex.StreamRequestPayload(request))
	stream, err := a.httpClient.StreamResponse(ctx, account, request, turnState)
	if err != nil {
		return account, nil, nil, err
	}
	return account, stream, codex.ParseQuotaFromHeaders(stream.Headers()), nil
}

func (a *App) openWSStream(c *gin.Context, ctx context.Context, endpoint string, normalized translate.NormalizedRequest, preferredID, turnState string) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
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
	a.logUpstreamPayload(c, endpoint, "websocket", account.ID, body)
	wsEndpoint := websocketEndpoint(a.cfg.CodexBaseURL)
	stream, err := a.wsClient.Connect(ctx, wsEndpoint, headers, body)
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
	toolCallInitialized := make(map[string]bool)
	toolCallArgumentsSent := make(map[string]int)
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
		if emitted := streamChatToolCallChunk(c.Writer, accumulator, normalized, event, toolCallIndex, toolCallInitialized, toolCallArgumentsSent, &nextToolCallIndex); emitted {
			c.Writer.Flush()
			continue
		}
		switch event.Type {
		case "response.reasoning_summary_text.delta":
			if normalized.Reasoning != nil {
				delta := jsonutil.StringValue(event.Raw["delta"])
				if delta != "" {
					writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{"reasoning_content": delta}, "")))
					c.Writer.Flush()
				}
			}
		case "response.output_text.delta":
			delta := jsonutil.StringValue(event.Raw["delta"])
			if delta == "" {
				continue
			}
			if normalized.TupleSchema != nil {
				tupleTextBuffer.WriteString(delta)
				continue
			}
			writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{"content": delta}, "")))
			c.Writer.Flush()
		case "response.output_text.done":
			if normalized.TupleSchema != nil {
				if text := jsonutil.StringValue(event.Raw["text"]); text != "" {
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
				writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{"content": reconverted}, "")))
				c.Writer.Flush()
			}
		}
		if event.Type == "response.completed" {
			break
		}
	}

	a.accounts.NoteSuccess(account.ID)
	a.rememberContinuation(account.ID, accumulator, stream.Headers().Get("x-codex-turn-state"))

	finalResponse := accumulator.ChatCompletionObject()
	finalUsage, _ := finalResponse["usage"].(map[string]any)
	writeSSE(c.Writer, "", translate.MustJSON(translate.ChatChunkWithUsage(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{}, chatStreamFinishReason(accumulator), finalUsage)))
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
		if normalized.TupleSchema != nil {
			switch event.Type {
			case "response.output_text.delta":
				tupleTextBuffer.WriteString(jsonutil.StringValue(event.Raw["delta"]))
				continue
			case "response.output_text.done":
				if text := jsonutil.StringValue(event.Raw["text"]); text != "" {
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
		if toolEvents, handled := accumulator.ResponsesStreamEventsForEvent(event); handled {
			writeResponseStreamEvents(c.Writer, accumulator.ResponseID, toolEvents)
			c.Writer.Flush()
			continue
		}
		if event.Type == "response.completed" {
			writeResponseStreamEvents(c.Writer, accumulator.ResponseID, accumulator.PendingResponseToolCallCompletionEvents())
			c.Writer.Flush()
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

func writeResponseStreamEvents(w io.Writer, responseID string, events []translate.ResponseStreamEvent) {
	for _, event := range events {
		writeSSE(w, event.Type, translate.ResponseEventJSON(event.Type, responseID, event.Payload))
	}
}

func streamChatToolCallChunk(w io.Writer, accumulator *translate.Accumulator, normalized translate.NormalizedRequest, event *codex.StreamEvent, toolCallIndex map[string]int, toolCallInitialized map[string]bool, toolCallArgumentsSent map[string]int, nextToolCallIndex *int) bool {
	if event == nil {
		return false
	}
	state := accumulator.ToolCallStateForEvent(event)
	if state == nil || strings.TrimSpace(state.CallID) == "" {
		return false
	}
	callID := state.CallID

	idx, exists := toolCallIndex[callID]
	if !exists {
		idx = *nextToolCallIndex
		toolCallIndex[callID] = idx
		*nextToolCallIndex = *nextToolCallIndex + 1
	}

	emitted := false
	if !toolCallInitialized[callID] && strings.TrimSpace(state.Name) != "" {
		writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{
			"tool_calls": []map[string]any{{
				"index": idx,
				"id":    callID,
				"type":  "function",
				"function": map[string]any{
					"name":      state.Name,
					"arguments": "",
				},
			}},
		}, "")))
		toolCallInitialized[callID] = true
		emitted = true
	}

	if !toolCallInitialized[callID] {
		return emitted
	}

	arguments := state.Arguments
	sent := toolCallArgumentsSent[callID]
	if sent >= len(arguments) {
		return emitted
	}

	writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{
		"tool_calls": []map[string]any{{
			"index": idx,
			"function": map[string]any{
				"arguments": arguments[sent:],
			},
		}},
	}, "")))
	toolCallArgumentsSent[callID] = len(arguments)
	return true
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

	payload := jsonutil.CloneMap(event.Raw)
	response, _ := payload["response"].(map[string]any)
	if response == nil {
		response = map[string]any{}
	}

	text := accumulator.Text()
	response["output"] = accumulator.ResponsesObject()["output"]
	if strings.TrimSpace(jsonutil.StringValue(response["output_text"])) == "" && strings.TrimSpace(text) != "" {
		response["output_text"] = text
	}
	if strings.TrimSpace(jsonutil.StringValue(response["status"])) == "" {
		response["status"] = "completed"
	}
	if accumulator.ResponseID != "" && strings.TrimSpace(jsonutil.StringValue(response["id"])) == "" {
		response["id"] = accumulator.ResponseID
	}
	if accumulator.Model != "" && strings.TrimSpace(jsonutil.StringValue(response["model"])) == "" {
		response["model"] = accumulator.Model
	}
	if rebuilt := accumulator.ResponsesUsageObject(); rebuilt != nil {
		response["usage"] = rebuilt
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
		Model:        jsonutil.FirstNonEmpty(accumulator.Model, accumulator.Normalized.Model),
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

	nested := jsonutil.MapValue(event.Raw, "error")
	if nested == nil {
		nested = jsonutil.PathMapValue(event.Raw, "response", "error")
	}
	message := jsonutil.FirstNonEmpty(
		jsonutil.StringValue(nested["message"]),
		jsonutil.StringValue(nested["detail"]),
		jsonutil.StringValue(event.Raw["message"]),
		jsonutil.StringValue(event.Raw["detail"]),
	)
	if message == "" {
		message = fmt.Sprintf("upstream %s", event.Type)
	}

	code := jsonutil.FirstNonEmpty(
		jsonutil.StringValue(nested["code"]),
		jsonutil.StringValue(nested["type"]),
		jsonutil.StringValue(event.Raw["code"]),
		jsonutil.StringValue(event.Raw["type"]),
	)
	statusCode, ok := 0, false
	if nested != nil {
		statusCode, ok = serverIntValue(nested["status_code"])
	}
	if !ok {
		if nested != nil {
			statusCode, ok = serverIntValue(nested["status"])
		}
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

func responseMapsFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func continuationInputHistory(accumulator *translate.Accumulator) []accounts.ContinuationInputItem {
	history := make([]accounts.ContinuationInputItem, 0, len(accumulator.Normalized.Input))
	for _, item := range accumulator.Normalized.Input {
		history = append(history, continuationInputItemFromCodex(item))
	}
	history = append(history, continuationOutputHistory(accumulator)...)
	return history
}

func continuationOutputHistory(accumulator *translate.Accumulator) []accounts.ContinuationInputItem {
	if accumulator == nil {
		return nil
	}
	response := accumulator.ResponsesObject()
	output, ok := response["output"].([]map[string]any)
	if !ok || len(output) == 0 {
		return nil
	}
	history := make([]accounts.ContinuationInputItem, 0, len(output))
	for _, item := range output {
		converted, ok := continuationInputItemFromResponseOutput(item)
		if ok {
			history = append(history, converted)
		}
	}
	return history
}

func continuationInputItemFromResponseOutput(item map[string]any) (accounts.ContinuationInputItem, bool) {
	if len(item) == 0 {
		return accounts.ContinuationInputItem{}, false
	}
	out := continuationInputItemBase(
		jsonutil.StringValue(item["role"]),
		jsonutil.StringValue(item["type"]),
		jsonutil.StringValue(item["call_id"]),
		jsonutil.StringValue(item["name"]),
		jsonutil.StringValue(item["arguments"]),
		jsonutil.StringValue(item["output"]),
		jsonutil.StringValue(item["id"]),
		jsonutil.StringValue(item["status"]),
		jsonutil.StringValue(item["encrypted_content"]),
	)
	out.Summary = continuationSummaryPartsFromMaps(responseMapsFromAny(item["summary"]))
	out.Content = continuationContentPartsFromMaps(responseMapsFromAny(item["content"]))
	out.OutputContent = continuationContentPartsFromMaps(responseMapsFromAny(item["output"]))
	if out.Role == "" && out.Type == "message" {
		out.Role = "assistant"
	}
	if out.Role == "" && out.Type == "" && len(out.Content) == 0 && len(out.OutputContent) == 0 && out.CallID == "" && out.ID == "" {
		return accounts.ContinuationInputItem{}, false
	}
	return out, true
}

func continuationInputItemsToCodex(items []accounts.ContinuationInputItem) []codex.InputItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]codex.InputItem, 0, len(items))
	for _, item := range items {
		out = append(out, continuationInputItemToCodex(item))
	}
	return out
}

func continuationInputItemFromCodex(item codex.InputItem) accounts.ContinuationInputItem {
	out := continuationInputItemBase(
		item.Role,
		item.Type,
		item.CallID,
		item.Name,
		item.Arguments,
		item.OutputText,
		item.ID,
		item.Status,
		item.EncryptedContent,
	)
	out.Summary = continuationSummaryPartsFromReasoning(item.Summary)
	out.Content = continuationContentPartsFromCodex(item.Content)
	out.OutputContent = continuationContentPartsFromCodex(item.OutputContent)
	return out
}

func continuationInputItemToCodex(item accounts.ContinuationInputItem) codex.InputItem {
	out := codex.InputItem{
		Role:             item.Role,
		Type:             item.Type,
		CallID:           item.CallID,
		Name:             item.Name,
		Arguments:        item.Arguments,
		OutputText:       item.OutputText,
		ID:               item.ID,
		Status:           item.Status,
		EncryptedContent: item.EncryptedContent,
	}
	out.Summary = reasoningPartsFromContinuation(item.Summary)
	out.Content = codexContentPartsFromContinuation(item.Content)
	out.OutputContent = codexContentPartsFromContinuation(item.OutputContent)
	return out
}

func continuationInputItemBase(role, itemType, callID, name, arguments, outputText, id, status, encryptedContent string) accounts.ContinuationInputItem {
	return accounts.ContinuationInputItem{
		Role:             role,
		Type:             itemType,
		CallID:           callID,
		Name:             name,
		Arguments:        arguments,
		OutputText:       outputText,
		ID:               id,
		Status:           status,
		EncryptedContent: encryptedContent,
	}
}

func continuationSummaryPartsFromMaps(parts []map[string]any) []accounts.ContinuationSummaryPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]accounts.ContinuationSummaryPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, accounts.ContinuationSummaryPart{
			Type: jsonutil.StringValue(part["type"]),
			Text: jsonutil.StringValue(part["text"]),
		})
	}
	return out
}

func continuationSummaryPartsFromReasoning(parts []openai.ReasoningPart) []accounts.ContinuationSummaryPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]accounts.ContinuationSummaryPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, accounts.ContinuationSummaryPart{
			Type: part.Type,
			Text: part.Text,
		})
	}
	return out
}

func reasoningPartsFromContinuation(parts []accounts.ContinuationSummaryPart) []openai.ReasoningPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]openai.ReasoningPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, openai.ReasoningPart{
			Type: part.Type,
			Text: part.Text,
		})
	}
	return out
}

func continuationContentPartsFromMaps(parts []map[string]any) []accounts.ContinuationContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]accounts.ContinuationContentPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, accounts.ContinuationContentPart{
			Type:     jsonutil.StringValue(part["type"]),
			Text:     jsonutil.StringValue(part["text"]),
			ImageURL: jsonutil.StringValue(part["image_url"]),
			Detail:   jsonutil.StringValue(part["detail"]),
			FileURL:  jsonutil.StringValue(part["file_url"]),
			FileData: jsonutil.StringValue(part["file_data"]),
			FileID:   jsonutil.StringValue(part["file_id"]),
			Filename: jsonutil.StringValue(part["filename"]),
		})
	}
	return out
}

func continuationContentPartsFromCodex(parts []codex.ContentPart) []accounts.ContinuationContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]accounts.ContinuationContentPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, accounts.ContinuationContentPart{
			Type:     part.Type,
			Text:     part.Text,
			ImageURL: part.ImageURL,
			Detail:   part.Detail,
			FileURL:  part.FileURL,
			FileData: part.FileData,
			FileID:   part.FileID,
			Filename: part.Filename,
		})
	}
	return out
}

func codexContentPartsFromContinuation(parts []accounts.ContinuationContentPart) []codex.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]codex.ContentPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, codex.ContentPart{
			Type:     part.Type,
			Text:     part.Text,
			ImageURL: part.ImageURL,
			Detail:   part.Detail,
			FileURL:  part.FileURL,
			FileData: part.FileData,
			FileID:   part.FileID,
			Filename: part.Filename,
		})
	}
	return out
}

func captureRequestBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func normalizeChatCompletionsBody(body []byte, defaultModel string) (translate.NormalizedRequest, error) {
	var chatReq openai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return translate.NormalizedRequest{}, err
	}

	if len(chatReq.Messages) > 0 {
		return translate.ChatCompletions(chatReq, defaultModel)
	}

	var envelope struct {
		Input              json.RawMessage `json:"input"`
		Instructions       json.RawMessage `json:"instructions"`
		PreviousResponseID string          `json:"previous_response_id"`
		Text               json.RawMessage `json:"text"`
		Reasoning          json.RawMessage `json:"reasoning"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return translate.NormalizedRequest{}, err
	}

	if len(bytes.TrimSpace(envelope.Input)) == 0 &&
		len(bytes.TrimSpace(envelope.Instructions)) == 0 &&
		strings.TrimSpace(envelope.PreviousResponseID) == "" &&
		len(bytes.TrimSpace(envelope.Text)) == 0 &&
		len(bytes.TrimSpace(envelope.Reasoning)) == 0 {
		return translate.ChatCompletions(chatReq, defaultModel)
	}

	var responsesReq openai.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		return translate.NormalizedRequest{}, err
	}

	normalized, err := translate.Responses(responsesReq, defaultModel)
	if err != nil {
		return translate.NormalizedRequest{}, err
	}
	normalized.Endpoint = translate.EndpointChat
	return normalized, nil
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
	quota := codex.ParseQuotaFromEvent(event, account.PlanType)
	a.observeQuotaSnapshot(account.ID, quota)
	return true
}

func (a *App) respondOpenAIInvalidRequest(c *gin.Context, err error) {
	a.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error(), "invalid_request_error")
}

func (a *App) handleOpenStreamError(c *gin.Context, endpoint, actualAccountID, reportedAccountID string, err error) {
	status, code, message := a.classifyUpstreamError(strings.TrimSpace(actualAccountID), err)
	logAccountID := jsonutil.FirstNonEmpty(actualAccountID, reportedAccountID)
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

func (a *App) setRequestAccount(c *gin.Context, account accounts.Record) {
	if c == nil || account.ID == "" {
		return
	}
	middleware.SetRequestAccount(c, account.ID, account.AccountID)
}
