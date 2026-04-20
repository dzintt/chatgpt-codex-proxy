package translate

import (
	"encoding/json"
	"fmt"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
)

type Accumulator struct {
	Normalized   NormalizedRequest
	ResponseID   string
	Model        string
	TextBuilder  strings.Builder
	Usage        *codex.Usage
	ToolCalls    []map[string]any
	toolCallByID map[string]map[string]any
	Output       []map[string]any
	Status       string
	RawFinal     map[string]any
}

func NewAccumulator(normalized NormalizedRequest) *Accumulator {
	return &Accumulator{
		Normalized:   normalized,
		toolCallByID: make(map[string]map[string]any),
	}
}

func (a *Accumulator) Apply(event *codex.StreamEvent) {
	if event == nil {
		return
	}
	if response := nestedMap(event.Raw, "response"); response != nil {
		if id := stringValue(response["id"]); id != "" {
			a.ResponseID = id
		}
		if model := stringValue(response["model"]); model != "" {
			a.Model = model
		}
		if status := stringValue(response["status"]); status != "" {
			a.Status = status
		}
		if usage := usageFromRaw(response["usage"]); usage != nil {
			a.Usage = usage
		}
		if output := sliceOfMaps(response["output"]); len(output) > 0 {
			a.Output = output
		}
	}
	if id := stringValue(event.Raw["response_id"]); id != "" && a.ResponseID == "" {
		a.ResponseID = id
	}
	if model := stringValue(event.Raw["model"]); model != "" && a.Model == "" {
		a.Model = model
	}
	switch event.Type {
	case "response.output_text.delta":
		if delta := stringValue(event.Raw["delta"]); delta != "" {
			a.TextBuilder.WriteString(delta)
		}
	case "response.output_text.done":
		if a.TextBuilder.Len() == 0 {
			a.TextBuilder.WriteString(stringValue(event.Raw["text"]))
		}
	case "response.content_part.done":
		if a.TextBuilder.Len() == 0 {
			part := nestedMap(event.Raw, "part")
			if text := stringValue(part["text"]); text != "" {
				a.TextBuilder.WriteString(text)
			}
		}
	case "response.completed":
		if a.TextBuilder.Len() == 0 {
			if response := nestedMap(event.Raw, "response"); response != nil {
				if text := stringValue(response["output_text"]); text != "" {
					a.TextBuilder.WriteString(text)
				}
			}
		}
	}
	if delta := firstString(
		stringValue(event.Raw["output_text"]),
		stringValue(nestedMap(event.Raw, "item")["text"]),
	); delta != "" && a.TextBuilder.Len() == 0 && strings.Contains(event.Type, "text") {
		a.TextBuilder.WriteString(delta)
	}
	if strings.HasPrefix(event.Type, "response.function_call_arguments.") {
		a.applyToolArgumentEvent(event)
	}
	if item := firstMap(nestedMap(event.Raw, "item"), nestedMap(event.Raw, "output_item")); item != nil {
		a.captureOutputItem(item)
	}
	if output := sliceOfMaps(event.Raw["output"]); len(output) > 0 {
		for _, item := range output {
			a.captureOutputItem(item)
		}
	}
	if usage := usageFromRaw(event.Raw["usage"]); usage != nil {
		a.Usage = usage
	}
	if event.Type == "response.completed" {
		a.RawFinal = event.Raw
		if response := nestedMap(event.Raw, "response"); response != nil {
			if usage := usageFromRaw(response["usage"]); usage != nil {
				a.Usage = usage
			}
			if output := sliceOfMaps(response["output"]); len(output) > 0 {
				a.Output = output
			}
		}
	}
}

func (a *Accumulator) Text() string {
	if text := strings.TrimSpace(a.TextBuilder.String()); text != "" {
		return text
	}
	for _, item := range a.Output {
		if itemType := stringValue(item["type"]); itemType == "message" {
			for _, content := range sliceOfMaps(item["content"]) {
				if text := stringValue(content["text"]); text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func (a *Accumulator) captureOutputItem(item map[string]any) {
	if len(item) == 0 {
		return
	}
	itemType := stringValue(item["type"])
	switch itemType {
	case "function_call":
		callID := firstString(stringValue(item["call_id"]), stringValue(item["id"]))
		responseItemID := stringValue(item["id"])
		call := map[string]any{
			"id":   callID,
			"type": "function",
			"function": map[string]any{
				"name":      stringValue(item["name"]),
				"arguments": stringValue(item["arguments"]),
			},
		}
		if existing := firstMap(a.toolCallByID[callID], a.toolCallByID[responseItemID]); existing != nil {
			mergeToolCall(existing, call)
			a.registerToolCallAliases(existing, callID, responseItemID)
			return
		}
		a.ToolCalls = append(a.ToolCalls, call)
		a.registerToolCallAliases(call, callID, responseItemID)
	case "message":
		if len(a.Output) == 0 {
			a.Output = append(a.Output, item)
		}
	default:
		if len(a.Output) == 0 {
			a.Output = append(a.Output, item)
		}
	}
}

func (a *Accumulator) ChatCompletionObject() map[string]any {
	message := map[string]any{
		"role":    "assistant",
		"content": a.Text(),
	}
	if len(a.ToolCalls) > 0 {
		message["tool_calls"] = a.ToolCalls
	}
	return map[string]any{
		"id":      firstString(a.ResponseID, "chatcmpl_proxy"),
		"object":  "chat.completion",
		"model":   firstString(a.Model, a.Normalized.Model),
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finishReason(a)}},
		"usage":   usageObject(a.Usage),
	}
}

func (a *Accumulator) ResponsesObject() map[string]any {
	text := a.Text()
	output := a.responsesOutput(text)
	return map[string]any{
		"id":          firstString(a.ResponseID, "resp_proxy"),
		"object":      "response",
		"model":       firstString(a.Model, a.Normalized.Model),
		"status":      firstString(a.Status, "completed"),
		"output":      output,
		"output_text": text,
		"usage":       usageObject(a.Usage),
	}
}

func (a *Accumulator) responsesOutput(text string) []map[string]any {
	output := make([]map[string]any, 0, len(a.Output)+len(a.ToolCalls)+1)
	for _, call := range a.ToolCalls {
		output = append(output, responseFunctionCallItem(call))
	}

	for _, item := range a.Output {
		cloned := cloneMap(item)
		if stringValue(cloned["type"]) == "message" {
			content := sliceOfMaps(cloned["content"])
			if len(content) == 0 && strings.TrimSpace(text) != "" {
				cloned["content"] = responseTextContent(text)
			}
			if stringValue(cloned["status"]) == "" {
				cloned["status"] = "completed"
			}
		}
		output = append(output, cloned)
	}

	if len(output) == 0 {
		output = append(output, map[string]any{
			"type":    "message",
			"role":    "assistant",
			"status":  "completed",
			"content": responseTextContent(text),
		})
	}
	return output
}

func responseTextContent(text string) []map[string]any {
	if strings.TrimSpace(text) == "" {
		return []map[string]any{}
	}
	return []map[string]any{{
		"type": "output_text",
		"text": text,
	}}
}

func ChatChunk(responseID, model string, delta map[string]any, finishReason string) map[string]any {
	choice := map[string]any{
		"index": 0,
		"delta": delta,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	}
	return map[string]any{
		"id":      firstString(responseID, "chatcmpl_proxy"),
		"object":  "chat.completion.chunk",
		"model":   model,
		"choices": []map[string]any{choice},
	}
}

func ResponseEventJSON(eventType string, responseID string, payload map[string]any) []byte {
	eventPayload := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		eventPayload[key] = value
	}
	if responseID != "" {
		eventPayload["response_id"] = responseID
	}
	eventPayload["type"] = eventType
	data, _ := json.Marshal(eventPayload)
	return data
}

func usageObject(usage *codex.Usage) any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
		"total_tokens":  usage.InputTokens + usage.OutputTokens,
	}
}

func finishReason(a *Accumulator) string {
	if len(a.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func (a *Accumulator) applyToolArgumentEvent(event *codex.StreamEvent) {
	responseItemID := stringValue(event.Raw["item_id"])
	callID := firstString(stringValue(event.Raw["call_id"]), responseItemID)
	if callID == "" {
		return
	}
	call := firstMap(a.toolCallByID[callID], a.toolCallByID[responseItemID])
	if call == nil {
		call = map[string]any{
			"id":   callID,
			"type": "function",
			"function": map[string]any{
				"name":      stringValue(event.Raw["name"]),
				"arguments": "",
			},
		}
		a.ToolCalls = append(a.ToolCalls, call)
	}
	a.registerToolCallAliases(call, callID, responseItemID)
	function, _ := call["function"].(map[string]any)
	if function == nil {
		function = map[string]any{}
		call["function"] = function
	}
	if name := stringValue(event.Raw["name"]); name != "" {
		function["name"] = name
	}
	switch event.Type {
	case "response.function_call_arguments.delta":
		function["arguments"] = stringValue(function["arguments"]) + stringValue(event.Raw["delta"])
	case "response.function_call_arguments.done":
		if args := stringValue(event.Raw["arguments"]); args != "" {
			function["arguments"] = args
		}
	}
}

func (a *Accumulator) registerToolCallAliases(call map[string]any, ids ...string) {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		a.toolCallByID[id] = call
	}
}

func responseFunctionCallItem(call map[string]any) map[string]any {
	function, _ := call["function"].(map[string]any)
	item := map[string]any{
		"type":      "function_call",
		"call_id":   stringValue(call["id"]),
		"name":      stringValue(function["name"]),
		"arguments": stringValue(function["arguments"]),
		"status":    "completed",
	}
	if id := stringValue(call["id"]); id != "" {
		item["id"] = id
	}
	return item
}

func mergeToolCall(dst, src map[string]any) {
	for key, value := range src {
		if key == "function" {
			dstFn, _ := dst["function"].(map[string]any)
			srcFn, _ := value.(map[string]any)
			if dstFn == nil {
				dstFn = map[string]any{}
			}
			for fnKey, fnValue := range srcFn {
				if fnValue == "" {
					continue
				}
				dstFn[fnKey] = fnValue
			}
			dst["function"] = dstFn
			continue
		}
		if value != "" {
			dst[key] = value
		}
	}
}

func nestedMap(raw map[string]any, key string) map[string]any {
	if raw == nil {
		return nil
	}
	value, ok := raw[key]
	if !ok {
		return nil
	}
	mapped, _ := value.(map[string]any)
	return mapped
}

func sliceOfMaps(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if ok {
			out = append(out, mapped)
		}
	}
	return out
}

func usageFromRaw(value any) *codex.Usage {
	if value == nil {
		return nil
	}
	switch mapped := value.(type) {
	case map[string]any:
		return &codex.Usage{
			InputTokens:  int64(numberValue(mapped["input_tokens"])),
			OutputTokens: int64(numberValue(mapped["output_tokens"])),
			CachedTokens: int64(numberValue(mapped["cached_tokens"])),
		}
	case codex.Usage:
		cloned := mapped
		return &cloned
	case *codex.Usage:
		return mapped
	default:
		return nil
	}
}

func stringValue(value any) string {
	str, _ := value.(string)
	return str
}

func numberValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		number, _ := typed.Float64()
		return number
	default:
		return 0
	}
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstMap(values ...map[string]any) map[string]any {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func MustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(fmt.Sprintf(`{"error":"%v"}`, err))
	}
	return data
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func ReconvertJSONText(text string, schema map[string]any) (string, error) {
	if schema == nil || strings.TrimSpace(text) == "" {
		return text, nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return text, err
	}

	reconverted := ReconvertTupleValues(decoded, schema)
	payload, err := json.Marshal(reconverted)
	if err != nil {
		return text, err
	}
	return string(payload), nil
}

func PatchChatCompletionObjectForTuple(object map[string]any, schema map[string]any) error {
	if schema == nil || len(object) == 0 {
		return nil
	}

	choices := sliceOfMaps(object["choices"])
	if len(choices) == 0 {
		return nil
	}

	message := nestedMap(choices[0], "message")
	if message == nil {
		return nil
	}

	reconverted, err := ReconvertJSONText(stringValue(message["content"]), schema)
	if err != nil {
		return err
	}
	message["content"] = reconverted
	return nil
}

func PatchResponsesObjectForTuple(object map[string]any, schema map[string]any) error {
	if schema == nil || len(object) == 0 {
		return nil
	}

	if text := stringValue(object["output_text"]); strings.TrimSpace(text) != "" {
		reconverted, err := ReconvertJSONText(text, schema)
		if err != nil {
			return err
		}
		object["output_text"] = reconverted
	}

	for _, item := range sliceOfMaps(object["output"]) {
		if stringValue(item["type"]) != "message" {
			continue
		}
		for _, content := range sliceOfMaps(item["content"]) {
			if stringValue(content["type"]) != "output_text" {
				continue
			}
			reconverted, err := ReconvertJSONText(stringValue(content["text"]), schema)
			if err != nil {
				return err
			}
			content["text"] = reconverted
		}
	}

	return nil
}

func PatchResponseCompletedPayloadForTuple(payload map[string]any, schema map[string]any) error {
	if schema == nil || len(payload) == 0 {
		return nil
	}

	response := nestedMap(payload, "response")
	if response == nil {
		return nil
	}

	if text := stringValue(response["output_text"]); strings.TrimSpace(text) != "" {
		reconverted, err := ReconvertJSONText(text, schema)
		if err != nil {
			return err
		}
		response["output_text"] = reconverted
	}

	for _, item := range sliceOfMaps(response["output"]) {
		if stringValue(item["type"]) != "message" {
			continue
		}
		for _, content := range sliceOfMaps(item["content"]) {
			if stringValue(content["type"]) != "output_text" {
				continue
			}
			reconverted, err := ReconvertJSONText(stringValue(content["text"]), schema)
			if err != nil {
				return err
			}
			content["text"] = reconverted
		}
	}

	return nil
}
