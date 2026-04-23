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
	"chatgpt-codex-proxy/internal/conversationkey"
	"chatgpt-codex-proxy/internal/jsonutil"
	"chatgpt-codex-proxy/internal/middleware"
	"chatgpt-codex-proxy/internal/models"
	"chatgpt-codex-proxy/internal/openai"
	"chatgpt-codex-proxy/internal/translate"
)

type eventStream interface {
	NextEvent() (*codex.StreamEvent, error)
	Close() error
	Headers() http.Header
}

type sessionResolution struct {
	Request            translate.NormalizedRequest
	Original           translate.NormalizedRequest
	PreferredAccountID string
	TurnState          string
	ConversationKey    string
	ExplicitPrevious   bool
	ImplicitResume     bool
}

var errIncompleteResponse = errors.New("upstream stream ended before response.completed")
var errEmptyChatCompletionsBody = errors.New("request body must include chat messages or responses input")

type openedRequest struct {
	Resolution sessionResolution
	Account    accounts.Record
	Stream     eventStream
}

func (a *App) handleChatCompletions(c *gin.Context) {
	a.handlePublicRequest(
		c,
		"chat_completions",
		func(body []byte) (translate.NormalizedRequest, error) {
			return normalizeChatCompletionsBody(body, a.cfg.DefaultModel, a.modelCatalog())
		},
		a.streamChatCompletion,
		func(accumulator *translate.Accumulator) map[string]any {
			return accumulator.ChatCompletionObject()
		},
		translate.PatchChatCompletionObjectForTuple,
	)
}

func (a *App) handleResponses(c *gin.Context) {
	a.handlePublicRequest(
		c,
		"responses",
		func(body []byte) (translate.NormalizedRequest, error) {
			return normalizeResponsesBody(body, a.cfg.DefaultModel, a.modelCatalog())
		},
		a.streamResponses,
		func(accumulator *translate.Accumulator) map[string]any {
			return accumulator.ResponsesObject()
		},
		translate.PatchResponsesObjectForTuple,
	)
}

func (a *App) handlePublicRequest(
	c *gin.Context,
	endpoint string,
	normalize func([]byte) (translate.NormalizedRequest, error),
	stream func(*gin.Context, accounts.Record, translate.NormalizedRequest, eventStream),
	buildResponse func(*translate.Accumulator) map[string]any,
	patchTuple func(map[string]any, map[string]any) error,
) {
	body, err := captureRequestBody(c)
	if err != nil {
		a.respondOpenAIInvalidRequest(c, err)
		return
	}
	a.logIncomingPayload(c, endpoint, body)

	normalized, err := normalize(body)
	if err != nil {
		a.respondOpenAINormalizeError(c, err)
		return
	}

	opened, ok := a.resolveAndOpenRequest(c, endpoint, normalized)
	if !ok {
		return
	}
	defer opened.Stream.Close()

	if opened.Resolution.Request.Stream {
		stream(c, opened.Account, opened.Resolution.Request, opened.Stream)
		return
	}

	accumulator, err := a.collectEvents(opened.Account, opened.Resolution.Request, opened.Stream)
	if err != nil {
		a.respondOpenAIUpstreamStreamError(c, endpoint, opened.Account.ID, "", err)
		return
	}
	response := buildResponse(accumulator)
	if err := patchTuple(response, normalized.TupleSchema); err != nil {
		a.logTupleReconversionWarning(c, endpoint, accumulator.ResponseID, err)
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) resolveAndOpenRequest(c *gin.Context, endpoint string, normalized translate.NormalizedRequest) (openedRequest, bool) {
	resolution, err := a.resolveSession(normalized)
	if err != nil {
		if a.writeRequestError(c, err) {
			return openedRequest{}, false
		}
		a.respondOpenAINormalizeError(c, err)
		return openedRequest{}, false
	}

	account, stream, quota, err := a.openStream(c, c.Request.Context(), endpoint, &resolution)
	if err != nil {
		a.setRequestAccount(c, account)
		reportedAccountID := jsonutil.FirstNonEmpty(account.ID, resolution.PreferredAccountID)
		a.handleOpenStreamError(c, endpoint, account.ID, reportedAccountID, err)
		return openedRequest{}, false
	}

	a.setRequestAccount(c, account)
	a.observeQuotaSnapshot(account.ID, quota)

	return openedRequest{
		Resolution: resolution,
		Account:    account,
		Stream:     stream,
	}, true
}

func (a *App) openStream(c *gin.Context, ctx context.Context, endpoint string, resolution *sessionResolution) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	if resolution.Request.PreviousResponseID != "" {
		account, stream, quota, err := a.openWSStream(c, ctx, endpoint, resolution)
		if err == nil || !resolution.ImplicitResume {
			return account, stream, quota, err
		}
		fallback := *resolution
		fallback.Request = resolution.Original
		fallback.PreferredAccountID = ""
		fallback.TurnState = ""
		fallback.ImplicitResume = false
		return a.openHTTPStream(c, ctx, endpoint, &fallback)
	}
	return a.openHTTPStream(c, ctx, endpoint, resolution)
}

func (a *App) openHTTPStream(c *gin.Context, ctx context.Context, endpoint string, resolution *sessionResolution) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.acquireAccountForResolution(ctx, resolution)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	request := resolution.Request.ToCodexRequest()
	a.logUpstreamPayload(c, endpoint, "http", account.ID, codex.StreamRequestPayload(request))
	stream, err := a.httpClient.StreamResponse(ctx, account, request, resolution.TurnState)
	if err != nil {
		return account, nil, nil, err
	}
	return account, stream, codex.ParseQuotaFromHeaders(stream.Headers()), nil
}

func (a *App) openWSStream(c *gin.Context, ctx context.Context, endpoint string, resolution *sessionResolution) (accounts.Record, eventStream, *accounts.QuotaSnapshot, error) {
	account, err := a.acquireAccountForResolution(ctx, resolution)
	if err != nil {
		return accounts.Record{}, nil, nil, err
	}
	headers := codex.BuildHeaders(a.cfg, account.Token.AccessToken, codex.HeaderOptions{
		AccountID:   account.AccountID,
		Cookies:     account.Cookies,
		TurnState:   resolution.TurnState,
		RequestID:   codex.NewRequestID(),
		IncludeBeta: true,
	})
	body := resolution.Request.ToCodexWSCreatePayload()
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
		event, _, err := a.nextStreamEvent(account, accumulator, stream)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if event.Type == "response.completed" {
			break
		}
	}

	if accumulator.ResponseID == "" || !accumulator.IsCompleted() {
		return nil, errIncompleteResponse
	}

	a.finalizeSuccessfulStream(account.ID, accumulator, stream)
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
		event, upstreamErr, err := a.nextStreamEvent(account, accumulator, stream)
		if err != nil {
			if err == io.EOF {
				if !accumulator.IsCompleted() {
					a.respondStreamError(c, "chat_completions", account.ID, accumulator.ResponseID, "", errIncompleteResponse)
					return
				}
				break
			}
			if upstreamErr {
				a.respondClassifiedStreamError(c, "chat_completions", account.ID, accumulator.ResponseID, "", err)
			} else {
				a.respondStreamError(c, "chat_completions", account.ID, accumulator.ResponseID, "", err)
			}
			return
		}
		if state := accumulator.ToolCallStateForEvent(event); state != nil && (state.ToolType == "custom" || strings.HasPrefix(event.Type, "response.custom_tool_call_input.")) {
			a.logCustomToolTrace(c, "chat_completions", "upstream_event", event.Type, state)
		}
		if emitted := streamChatToolCallChunk(c.Writer, accumulator, normalized, event, toolCallIndex, toolCallInitialized, toolCallArgumentsSent, &nextToolCallIndex); emitted {
			if state := accumulator.ToolCallStateForEvent(event); state != nil && state.ToolType == "custom" {
				a.logCustomToolTrace(c, "chat_completions", "chat_chunk_emitted", event.Type, state)
			}
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

	a.finalizeSuccessfulStream(account.ID, accumulator, stream)

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
		event, upstreamErr, err := a.nextStreamEvent(account, accumulator, stream)
		if err != nil {
			if err == io.EOF {
				if !accumulator.IsCompleted() {
					a.respondStreamError(c, "responses", account.ID, accumulator.ResponseID, "error", errIncompleteResponse)
					return
				}
				break
			}
			if upstreamErr {
				a.respondClassifiedStreamError(c, "responses", account.ID, accumulator.ResponseID, "error", err)
			} else {
				a.respondStreamError(c, "responses", account.ID, accumulator.ResponseID, "error", err)
			}
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

	a.finalizeSuccessfulStream(account.ID, accumulator, stream)
	writeSSE(c.Writer, "done", []byte("[DONE]"))
	c.Writer.Flush()
}

func (a *App) nextStreamEvent(account accounts.Record, accumulator *translate.Accumulator, stream eventStream) (*codex.StreamEvent, bool, error) {
	for {
		event, err := stream.NextEvent()
		if err != nil {
			return nil, false, err
		}
		if a.observeQuotaEvent(account, event) {
			continue
		}
		accumulator.Apply(event)
		if upstreamErr := upstreamEventError(event); upstreamErr != nil {
			return nil, true, upstreamErr
		}
		return event, false, nil
	}
}

func (a *App) finalizeSuccessfulStream(accountID string, accumulator *translate.Accumulator, stream eventStream) {
	a.accounts.NoteSuccess(accountID)
	a.rememberContinuation(accountID, accumulator, stream.Headers().Get("x-codex-turn-state"))
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
		// Chat Completions clients like Cursor reliably understand function-call
		// deltas but may reject streamed custom-tool deltas. We expose every tool
		// call as function-shaped here and map custom tools back upstream on replay.
		chunkToolCall := map[string]any{
			"index": idx,
			"id":    callID,
		}
		chunkToolCall["type"] = "function"
		chunkToolCall["function"] = map[string]any{
			"name":      state.Name,
			"arguments": "",
		}
		writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{
			"tool_calls": []map[string]any{chunkToolCall},
		}, "")))
		toolCallInitialized[callID] = true
		emitted = true
	}

	if !toolCallInitialized[callID] {
		return emitted
	}

	value := state.Arguments
	fieldName := "arguments"
	parentField := "function"
	if state.ToolType == "custom" {
		value = state.Input
	}
	sent := toolCallArgumentsSent[callID]
	if sent >= len(value) {
		return emitted
	}

	writeSSE(w, "", translate.MustJSON(translate.ChatChunk(accumulator.ResponseID, jsonutil.FirstNonEmpty(accumulator.Model, normalized.Model), map[string]any{
		"tool_calls": []map[string]any{{
			"index": idx,
			parentField: map[string]any{
				fieldName: value[sent:],
			},
		}},
	}, "")))
	toolCallArgumentsSent[callID] = len(value)
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
	if accumulator == nil || accumulator.ResponseID == "" {
		return
	}
	conversationKey := accumulator.Normalized.PromptCacheKey
	if strings.TrimSpace(conversationKey) == "" {
		conversationKey = resolutionConversationKey(accumulator.Normalized)
	}
	a.continuations.Put(accounts.ContinuationRecord{
		ResponseID:      accumulator.ResponseID,
		AccountID:       accountID,
		UpstreamID:      accumulator.ResponseID,
		ConversationKey: conversationKey,
		TurnState:       strings.TrimSpace(turnState),
		Instructions:    strings.TrimSpace(accumulator.Normalized.Instructions),
		Model:           jsonutil.FirstNonEmpty(accumulator.Model, accumulator.Normalized.Model),
		InputHistory:    continuationInputHistory(accumulator),
		FunctionCallIDs: functionCallIDs(accumulator),
		CreatedAt:       timeNowUTC(),
		ExpiresAt:       timeNowUTC().Add(a.cfg.ContinuationTTL),
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
		statusCode, _ = serverIntValue(event.Raw["status"])
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
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed), true
		}
		floatValue, floatErr := typed.Float64()
		if floatErr == nil {
			return int(floatValue), true
		}
		return 0, false
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
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
		jsonutil.StringValue(item["phase"]),
		jsonutil.StringValue(item["call_id"]),
		jsonutil.StringValue(item["name"]),
		jsonutil.StringValue(item["input"]),
		jsonutil.StringValue(item["arguments"]),
		jsonutil.StringValue(item["output"]),
		jsonutil.StringValue(item["id"]),
		jsonutil.StringValue(item["status"]),
		jsonutil.StringValue(item["encrypted_content"]),
	)
	out.Summary = continuationSummaryPartsFromMaps(jsonutil.SliceOfMaps(item["summary"]))
	out.Content = continuationContentPartsFromMaps(jsonutil.SliceOfMaps(item["content"]))
	out.OutputContent = continuationContentPartsFromMaps(jsonutil.SliceOfMaps(item["output"]))
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
		item.Phase,
		item.CallID,
		item.Name,
		item.Input,
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
		Phase:            item.Phase,
		CallID:           item.CallID,
		Name:             item.Name,
		Input:            item.Input,
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

func continuationInputItemBase(role, itemType, phase, callID, name, input, arguments, outputText, id, status, encryptedContent string) accounts.ContinuationInputItem {
	return accounts.ContinuationInputItem{
		Role:             role,
		Type:             itemType,
		Phase:            phase,
		CallID:           callID,
		Name:             name,
		Input:            input,
		Arguments:        arguments,
		OutputText:       outputText,
		ID:               id,
		Status:           status,
		EncryptedContent: encryptedContent,
	}
}

func continuationSummaryPartsFromMaps(parts []map[string]any) []accounts.ContinuationSummaryPart {
	return mapParts(parts, func(part map[string]any) accounts.ContinuationSummaryPart {
		return accounts.ContinuationSummaryPart{
			Type: jsonutil.StringValue(part["type"]),
			Text: jsonutil.StringValue(part["text"]),
		}
	})
}

func continuationSummaryPartsFromReasoning(parts []openai.ReasoningPart) []accounts.ContinuationSummaryPart {
	return cloneParts(parts)
}

func reasoningPartsFromContinuation(parts []accounts.ContinuationSummaryPart) []openai.ReasoningPart {
	return cloneParts(parts)
}

func continuationContentPartsFromMaps(parts []map[string]any) []accounts.ContinuationContentPart {
	return mapParts(parts, func(part map[string]any) accounts.ContinuationContentPart {
		return accounts.ContinuationContentPart{
			Type:     jsonutil.StringValue(part["type"]),
			Text:     jsonutil.StringValue(part["text"]),
			ImageURL: jsonutil.StringValue(part["image_url"]),
			Detail:   jsonutil.StringValue(part["detail"]),
			FileURL:  jsonutil.StringValue(part["file_url"]),
			FileData: jsonutil.StringValue(part["file_data"]),
			FileID:   jsonutil.StringValue(part["file_id"]),
			Filename: jsonutil.StringValue(part["filename"]),
		}
	})
}

func continuationContentPartsFromCodex(parts []codex.ContentPart) []accounts.ContinuationContentPart {
	return cloneParts(parts)
}

func codexContentPartsFromContinuation(parts []accounts.ContinuationContentPart) []codex.ContentPart {
	return cloneParts(parts)
}

func mapParts[T any](parts []map[string]any, fn func(map[string]any) T) []T {
	if len(parts) == 0 {
		return nil
	}
	out := make([]T, 0, len(parts))
	for _, part := range parts {
		out = append(out, fn(part))
	}
	return out
}

func cloneParts[T any](parts []T) []T {
	if len(parts) == 0 {
		return nil
	}
	return append([]T(nil), parts...)
}

func captureRequestBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return nil, fmt.Errorf("request body is unavailable")
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func normalizeChatCompletionsBody(body []byte, defaultModel string, catalog *models.Catalog) (translate.NormalizedRequest, error) {
	var chatReq openai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return translate.NormalizedRequest{}, err
	}

	if len(chatReq.Messages) > 0 {
		return translate.ChatCompletions(chatReq, defaultModel, catalog)
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
		return translate.NormalizedRequest{}, errEmptyChatCompletionsBody
	}

	var responsesReq openai.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		return translate.NormalizedRequest{}, err
	}

	normalized, err := translate.Responses(responsesReq, defaultModel, catalog)
	if err != nil {
		return translate.NormalizedRequest{}, err
	}
	normalized.Endpoint = translate.EndpointChat
	return normalized, nil
}

func normalizeResponsesBody(body []byte, defaultModel string, catalog *models.Catalog) (translate.NormalizedRequest, error) {
	var req openai.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return translate.NormalizedRequest{}, err
	}
	return translate.Responses(req, defaultModel, catalog)
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

func (a *App) respondOpenAINormalizeError(c *gin.Context, err error) {
	var modelErr *translate.ModelNotFoundError
	if errors.As(err, &modelErr) {
		message := "Model '" + strings.TrimSpace(modelErr.Model) + "' not found"
		a.writeOpenAIError(c, http.StatusNotFound, "model_not_found", message, "invalid_request_error")
		return
	}
	var contentErr *translate.UnsupportedContentPartError
	if errors.As(err, &contentErr) {
		a.writeOpenAIError(c, http.StatusBadRequest, "unsupported_content_part", contentErr.Error(), "invalid_request_error")
		return
	}
	a.respondOpenAIInvalidRequest(c, err)
}

func (a *App) handleOpenStreamError(c *gin.Context, endpoint, actualAccountID, reportedAccountID string, err error) {
	if errors.Is(err, errContinuationAccountUnavailable) {
		a.writeOpenAIError(c, http.StatusServiceUnavailable, "continuation_account_unavailable", "continuation account unavailable", "api_error")
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "no active accounts") {
		a.writeOpenAIError(c, http.StatusServiceUnavailable, "no_available_accounts", "no available accounts", "api_error")
		return
	}
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

func (a *App) acquireReadyAccount(ctx context.Context, preferredID, modelID string) (accounts.Record, error) {
	return a.accountMgr.AcquireReadyForModel(ctx, preferredID, modelID)
}

func (a *App) acquireAccountForResolution(ctx context.Context, resolution *sessionResolution) (accounts.Record, error) {
	if resolution == nil {
		return accounts.Record{}, errContinuationAccountUnavailable
	}
	if resolution.ExplicitPrevious || resolution.ImplicitResume {
		preferredID := strings.TrimSpace(resolution.PreferredAccountID)
		if preferredID == "" {
			return accounts.Record{}, errContinuationAccountUnavailable
		}
		record, err := a.accountMgr.EnsureReady(ctx, preferredID)
		if err != nil {
			return accounts.Record{}, errContinuationAccountUnavailable
		}
		if !a.modelCatalog().SupportsRecord(record, resolution.Request.Model) {
			return accounts.Record{}, errContinuationAccountUnavailable
		}
		return record, nil
	}
	if !resolution.Request.ModelExplicit && strings.TrimSpace(resolution.Request.Model) == "" {
		record, err := a.accountMgr.AcquireReady(ctx, resolution.PreferredAccountID)
		if err != nil {
			return accounts.Record{}, err
		}
		modelID := a.modelCatalog().ResolveDefaultForRecord(record, a.cfg.DefaultModel)
		if strings.TrimSpace(modelID) == "" {
			return accounts.Record{}, errContinuationAccountUnavailable
		}
		resolution.Request.Model = modelID
		resolution.Original.Model = modelID
		if key := conversationkey.Derive(resolution.Request.ToCodexRequest()); key != "" {
			resolution.ConversationKey = key
			resolution.Request.PromptCacheKey = key
			resolution.Original.PromptCacheKey = key
		}
		return record, nil
	}
	return a.acquireReadyAccount(ctx, resolution.PreferredAccountID, resolution.Request.Model)
}

func (a *App) setRequestAccount(c *gin.Context, account accounts.Record) {
	if c == nil || account.ID == "" {
		return
	}
	middleware.SetRequestAccount(c, account.ID, account.AccountID)
}
