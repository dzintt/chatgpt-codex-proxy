package server

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/conversationkey"
	"chatgpt-codex-proxy/internal/translate"
)

var errContinuationAccountUnavailable = errors.New("continuation account unavailable")

func (a *App) resolveSession(normalized translate.NormalizedRequest) (sessionResolution, error) {
	resolution := sessionResolution{
		Request:  normalized,
		Original: normalized,
	}

	if normalized.PreviousResponseID != "" {
		record, ok := a.continuations.Get(normalized.PreviousResponseID)
		if !ok {
			return sessionResolution{}, invalidPreviousResponseIDError()
		}
		if strings.TrimSpace(resolution.Request.Model) == "" {
			resolution.Request.Model = record.Model
			resolution.Original.Model = record.Model
		}
		if key := strings.TrimSpace(record.ConversationKey); key != "" {
			resolution.ConversationKey = key
			resolution.Request.PromptCacheKey = key
			resolution.Original.PromptCacheKey = key
		} else if key := conversationkey.Derive(resolution.Request.ToCodexRequest()); key != "" {
			resolution.ConversationKey = key
			resolution.Request.PromptCacheKey = key
			resolution.Original.PromptCacheKey = key
		}
		resolution.PreferredAccountID = record.AccountID
		resolution.TurnState = strings.TrimSpace(record.TurnState)
		resolution.ExplicitPrevious = true
		return resolution, nil
	}

	if strings.TrimSpace(resolution.Request.Model) == "" {
		return resolution, nil
	}

	if key := conversationkey.Derive(normalized.ToCodexRequest()); key != "" {
		resolution.ConversationKey = key
		resolution.Request.PromptCacheKey = key
		resolution.Original.PromptCacheKey = key
	}

	if resolution.ConversationKey == "" {
		return resolution, nil
	}
	for _, record := range a.continuations.ListByConversation(resolution.ConversationKey) {
		if !canImplicitlyResume(record, normalized) {
			continue
		}
		trimmed, ok := trimmedContinuationInput(normalized.Input, record)
		if !ok {
			continue
		}

		resolution.Request.Input = trimmed
		resolution.Request.PreviousResponseID = record.ResponseID
		resolution.Request.Model = record.Model
		resolution.Original.Model = record.Model
		resolution.PreferredAccountID = record.AccountID
		resolution.TurnState = strings.TrimSpace(record.TurnState)
		resolution.ImplicitResume = true
		return resolution, nil
	}
	return resolution, nil
}

func invalidPreviousResponseIDError() error {
	return &upstreamLikeRequestError{
		status:  http.StatusBadRequest,
		code:    "invalid_previous_response_id",
		message: "unknown or expired previous_response_id",
		errType: "invalid_request_error",
	}
}

type upstreamLikeRequestError struct {
	status  int
	code    string
	message string
	errType string
}

func (e *upstreamLikeRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func canImplicitlyResume(record accounts.ContinuationRecord, normalized translate.NormalizedRequest) bool {
	if strings.TrimSpace(record.ResponseID) == "" {
		return false
	}
	if strings.TrimSpace(record.Model) != strings.TrimSpace(normalized.Model) {
		return false
	}
	if strings.TrimSpace(record.Instructions) != strings.TrimSpace(normalized.Instructions) {
		return false
	}
	return hasPriorAssistantOrToolHistory(normalized.Input)
}

func hasPriorAssistantOrToolHistory(input []codex.InputItem) bool {
	for _, item := range input {
		if item.Role == "assistant" {
			return true
		}
		switch item.Type {
		case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
			return true
		}
	}
	return false
}

func trimmedContinuationInput(input []codex.InputItem, record accounts.ContinuationRecord) ([]codex.InputItem, bool) {
	if len(input) == 0 {
		return nil, false
	}

	history := continuationInputItemsToCodex(record.InputHistory)
	if len(history) == 0 || len(input) <= len(history) {
		return nil, false
	}
	for idx := range history {
		if !reflect.DeepEqual(input[idx], history[idx]) {
			return nil, false
		}
	}

	allowedCallIDs := make(map[string]struct{}, len(record.FunctionCallIDs))
	for _, callID := range record.FunctionCallIDs {
		callID = strings.TrimSpace(callID)
		if callID != "" {
			allowedCallIDs[callID] = struct{}{}
		}
	}

	trimmed := append([]codex.InputItem(nil), input[len(history):]...)
	for _, item := range trimmed {
		switch item.Type {
		case "function_call_output", "custom_tool_call_output":
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				return nil, false
			}
			if _, ok := allowedCallIDs[callID]; !ok {
				return nil, false
			}
		}
	}
	return trimmed, true
}

func (a *App) writeRequestError(c *gin.Context, err error) bool {
	var requestErr *upstreamLikeRequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	a.writeOpenAIError(c, requestErr.status, requestErr.code, requestErr.message, requestErr.errType)
	return true
}

func functionCallIDs(accumulator *translate.Accumulator) []string {
	if accumulator == nil || len(accumulator.ToolCalls) == 0 {
		return nil
	}
	ids := make([]string, 0, len(accumulator.ToolCalls))
	for _, call := range accumulator.ToolCalls {
		callID := strings.TrimSpace(call.CallID)
		if callID == "" || slices.Contains(ids, callID) {
			continue
		}
		ids = append(ids, callID)
	}
	return ids
}

func resolutionConversationKey(normalized translate.NormalizedRequest) string {
	if key := strings.TrimSpace(normalized.PromptCacheKey); key != "" {
		return key
	}
	return conversationkey.Derive(normalized.ToCodexRequest())
}
