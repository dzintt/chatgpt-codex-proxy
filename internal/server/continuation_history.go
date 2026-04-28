package server

import (
	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/jsonutil"
	"chatgpt-codex-proxy/internal/openai"
	"chatgpt-codex-proxy/internal/translate"
)

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
	if out.Type == "message" {
		if out.Role == "" {
			out.Role = "assistant"
		}
		out.Type = ""
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
