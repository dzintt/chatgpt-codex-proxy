package translate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/jsonutil"
)

type ToolCallState struct {
	ItemID           string
	CallID           string
	Name             string
	Arguments        string
	OutputIndex      int
	Status           string
	AddedEmitted     bool
	DoneEmitted      bool
	SawArgumentDelta bool
}

type ResponseStreamEvent struct {
	Type    string
	Payload map[string]any
}

type outputItemState struct {
	Key         string
	OutputIndex int
	Item        map[string]any
}

type Accumulator struct {
	Normalized      NormalizedRequest
	ResponseID      string
	Model           string
	TextBuilder     strings.Builder
	Usage           *codex.Usage
	ToolCalls       []*ToolCallState
	toolCallByID    map[string]*ToolCallState
	OutputItems     []*outputItemState
	outputItemByKey map[string]*outputItemState
	Status          string
	RawFinal        map[string]any
	nextOutputIndex int
}

func NewAccumulator(normalized NormalizedRequest) *Accumulator {
	return &Accumulator{
		Normalized:      normalized,
		toolCallByID:    make(map[string]*ToolCallState),
		outputItemByKey: make(map[string]*outputItemState),
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
			a.replaceOutputItems(output)
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
		a.captureOutputItem(item, outputIndexFromMap(event.Raw))
	}
	if output := sliceOfMaps(event.Raw["output"]); len(output) > 0 {
		a.replaceOutputItems(output)
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
				a.replaceOutputItems(output)
			}
		}
	}
}

func (a *Accumulator) Text() string {
	if text := strings.TrimSpace(a.TextBuilder.String()); text != "" {
		return text
	}
	for _, item := range a.sortedOutputItems() {
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

func (a *Accumulator) IsCompleted() bool {
	return a != nil && a.RawFinal != nil
}

func (a *Accumulator) ResponsesStreamEventsForEvent(event *codex.StreamEvent) ([]ResponseStreamEvent, bool) {
	if event == nil {
		return nil, false
	}

	switch event.Type {
	case "response.function_call_arguments.delta":
		state := a.toolCallStateForEvent(event)
		if state == nil {
			return nil, false
		}
		events := a.ensureResponseOutputItemAdded(state)
		if delta := stringValue(event.Raw["delta"]); delta != "" {
			events = append(events, ResponseStreamEvent{
				Type: "response.function_call_arguments.delta",
				Payload: map[string]any{
					"item_id":      state.ItemID,
					"output_index": state.OutputIndex,
					"delta":        delta,
				},
			})
		}
		return events, true
	case "response.function_call_arguments.done":
		state := a.toolCallStateForEvent(event)
		if state == nil {
			return nil, false
		}
		return a.ensureResponseToolCallCompleted(state), true
	case "response.output_item.added":
		state := a.toolCallStateForEvent(event)
		if state == nil {
			return nil, false
		}
		return a.ensureResponseOutputItemAdded(state), true
	case "response.output_item.done":
		state := a.toolCallStateForEvent(event)
		if state == nil {
			return nil, false
		}
		return a.ensureResponseToolCallCompleted(state), true
	default:
		return nil, false
	}
}

func (a *Accumulator) PendingResponseToolCallCompletionEvents() []ResponseStreamEvent {
	events := make([]ResponseStreamEvent, 0)
	for _, state := range a.ToolCalls {
		if state.DoneEmitted {
			continue
		}
		events = append(events, a.ensureResponseToolCallCompleted(state)...)
	}
	return events
}

func (a *Accumulator) captureOutputItem(item map[string]any, explicitIndex int) {
	if len(item) == 0 {
		return
	}

	itemType := stringValue(item["type"])
	if itemType == "function_call" {
		callID := firstString(stringValue(item["call_id"]), stringValue(item["id"]))
		itemID := firstString(stringValue(item["id"]), callID)
		if callID == "" && itemID == "" {
			return
		}
		state := a.ensureToolCallState(itemID, callID, explicitIndex)
		if state == nil {
			return
		}
		if name := stringValue(item["name"]); name != "" {
			state.Name = name
		}
		if arguments := stringValue(item["arguments"]); arguments != "" {
			state.Arguments = arguments
		}
		if status := stringValue(item["status"]); status != "" {
			state.Status = status
		}
		return
	}

	index := a.resolveOutputIndex(explicitIndex)
	key := outputItemKey(item, index)
	cloned := cloneMap(item)
	if existing, ok := a.outputItemByKey[key]; ok {
		existing.Item = cloned
		existing.OutputIndex = index
		return
	}
	state := &outputItemState{
		Key:         key,
		OutputIndex: index,
		Item:        cloned,
	}
	a.OutputItems = append(a.OutputItems, state)
	a.outputItemByKey[key] = state
}

func (a *Accumulator) replaceOutputItems(items []map[string]any) {
	a.OutputItems = nil
	a.outputItemByKey = make(map[string]*outputItemState)
	for idx, item := range items {
		a.captureOutputItem(item, idx)
	}
}

func (a *Accumulator) ChatCompletionObject() map[string]any {
	message := map[string]any{
		"role":    "assistant",
		"content": a.Text(),
	}
	if toolCalls := a.chatCompletionToolCalls(); len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
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
	type outputEntry struct {
		OutputIndex int
		Order       int
		Item        map[string]any
	}

	entries := make([]outputEntry, 0, len(a.ToolCalls)+len(a.OutputItems))
	for order, state := range a.ToolCalls {
		entries = append(entries, outputEntry{
			OutputIndex: state.OutputIndex,
			Order:       order,
			Item:        state.responseOutputItem("completed"),
		})
	}

	baseOrder := len(entries)
	for order, state := range a.OutputItems {
		cloned := cloneMap(state.Item)
		if stringValue(cloned["type"]) == "message" {
			content := sliceOfMaps(cloned["content"])
			if len(content) == 0 && strings.TrimSpace(text) != "" {
				cloned["content"] = responseTextContent(text)
			}
			if stringValue(cloned["status"]) == "" {
				cloned["status"] = "completed"
			}
		}
		entries = append(entries, outputEntry{
			OutputIndex: state.OutputIndex,
			Order:       baseOrder + order,
			Item:        cloned,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].OutputIndex == entries[j].OutputIndex {
			return entries[i].Order < entries[j].Order
		}
		return entries[i].OutputIndex < entries[j].OutputIndex
	})

	output := make([]map[string]any, 0, len(entries)+1)
	for _, entry := range entries {
		output = append(output, entry.Item)
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

func (a *Accumulator) chatCompletionToolCalls() []map[string]any {
	if len(a.ToolCalls) == 0 {
		return nil
	}

	out := make([]map[string]any, 0, len(a.ToolCalls))
	for _, state := range a.ToolCalls {
		out = append(out, state.chatCompletionToolCall())
	}
	return out
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

func usageObject(usage *codex.Usage) *UsageSummary {
	if usage == nil {
		return nil
	}
	return &UsageSummary{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.InputTokens + usage.OutputTokens,
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
	if callID == "" && responseItemID == "" {
		return
	}

	state := a.ensureToolCallState(responseItemID, callID, outputIndexFromMap(event.Raw))
	if state == nil {
		return
	}
	if name := stringValue(event.Raw["name"]); name != "" {
		state.Name = name
	}

	switch event.Type {
	case "response.function_call_arguments.delta":
		state.Arguments += stringValue(event.Raw["delta"])
		state.SawArgumentDelta = true
	case "response.function_call_arguments.done":
		if args := stringValue(event.Raw["arguments"]); args != "" {
			state.Arguments = args
		}
		state.Status = "completed"
	}
}

func (a *Accumulator) ensureToolCallState(itemID, callID string, explicitIndex int) *ToolCallState {
	itemID = strings.TrimSpace(itemID)
	callID = strings.TrimSpace(callID)
	if itemID == "" {
		itemID = callID
	}
	if callID == "" {
		callID = itemID
	}
	if itemID == "" || callID == "" {
		return nil
	}

	if existing := firstToolCallState(a.toolCallByID[callID], a.toolCallByID[itemID]); existing != nil {
		if explicitIndex >= 0 {
			existing.OutputIndex = a.resolveOutputIndex(explicitIndex)
		}
		existing.ItemID = firstString(existing.ItemID, itemID)
		existing.CallID = firstString(existing.CallID, callID)
		a.registerToolCallAliases(existing, existing.CallID, existing.ItemID)
		return existing
	}

	state := &ToolCallState{
		ItemID:      itemID,
		CallID:      callID,
		OutputIndex: a.resolveOutputIndex(explicitIndex),
		Status:      "in_progress",
	}
	a.ToolCalls = append(a.ToolCalls, state)
	a.registerToolCallAliases(state, callID, itemID)
	return state
}

func (a *Accumulator) registerToolCallAliases(call *ToolCallState, ids ...string) {
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		a.toolCallByID[id] = call
	}
}

func (a *Accumulator) toolCallStateForEvent(event *codex.StreamEvent) *ToolCallState {
	if event == nil {
		return nil
	}

	switch event.Type {
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		itemID := stringValue(event.Raw["item_id"])
		callID := firstString(stringValue(event.Raw["call_id"]), itemID)
		return firstToolCallState(a.toolCallByID[callID], a.toolCallByID[itemID])
	case "response.output_item.added", "response.output_item.done":
		item := firstMap(nestedMap(event.Raw, "item"), nestedMap(event.Raw, "output_item"))
		if stringValue(item["type"]) != "function_call" {
			return nil
		}
		itemID := firstString(stringValue(item["id"]), stringValue(event.Raw["item_id"]))
		callID := firstString(stringValue(item["call_id"]), itemID)
		return firstToolCallState(a.toolCallByID[callID], a.toolCallByID[itemID])
	default:
		return nil
	}
}

func (a *Accumulator) ensureResponseOutputItemAdded(state *ToolCallState) []ResponseStreamEvent {
	if state == nil || state.AddedEmitted {
		return nil
	}
	state.AddedEmitted = true
	state.Status = firstString(state.Status, "in_progress")
	return []ResponseStreamEvent{{
		Type: "response.output_item.added",
		Payload: map[string]any{
			"output_index": state.OutputIndex,
			"item":         state.responseOutputItem("in_progress"),
		},
	}}
}

func (a *Accumulator) ensureResponseToolCallCompleted(state *ToolCallState) []ResponseStreamEvent {
	if state == nil || state.DoneEmitted {
		return nil
	}

	events := a.ensureResponseOutputItemAdded(state)
	events = append(events, ResponseStreamEvent{
		Type: "response.function_call_arguments.done",
		Payload: map[string]any{
			"item_id":      state.ItemID,
			"output_index": state.OutputIndex,
			"name":         state.Name,
			"arguments":    state.Arguments,
		},
	})
	state.Status = "completed"
	state.DoneEmitted = true
	events = append(events, ResponseStreamEvent{
		Type: "response.output_item.done",
		Payload: map[string]any{
			"output_index": state.OutputIndex,
			"item":         state.responseOutputItem("completed"),
		},
	})
	return events
}

func (a *Accumulator) resolveOutputIndex(preferred int) int {
	if preferred >= 0 {
		if preferred >= a.nextOutputIndex {
			a.nextOutputIndex = preferred + 1
		}
		return preferred
	}
	index := a.nextOutputIndex
	a.nextOutputIndex++
	return index
}

func (a *Accumulator) sortedOutputItems() []map[string]any {
	if len(a.OutputItems) == 0 {
		return nil
	}

	states := append([]*outputItemState(nil), a.OutputItems...)
	sort.SliceStable(states, func(i, j int) bool {
		return states[i].OutputIndex < states[j].OutputIndex
	})

	items := make([]map[string]any, 0, len(states))
	for _, state := range states {
		items = append(items, state.Item)
	}
	return items
}

func (t *ToolCallState) chatCompletionToolCall() map[string]any {
	if t == nil {
		return nil
	}
	return map[string]any{
		"id":   t.CallID,
		"type": "function",
		"function": map[string]any{
			"name":      t.Name,
			"arguments": t.Arguments,
		},
	}
}

func (t *ToolCallState) responseOutputItem(status string) map[string]any {
	if t == nil {
		return nil
	}
	itemStatus := firstString(status, t.Status, "completed")
	return map[string]any{
		"type":      "function_call",
		"id":        t.ItemID,
		"call_id":   t.CallID,
		"name":      t.Name,
		"arguments": t.Arguments,
		"status":    itemStatus,
	}
}

func outputIndexFromMap(raw map[string]any) int {
	if raw == nil {
		return -1
	}
	if value, ok := intValue(raw["output_index"]); ok {
		return value
	}
	return -1
}

func outputItemKey(item map[string]any, outputIndex int) string {
	if id := stringValue(item["id"]); id != "" {
		return "id:" + id
	}
	if outputIndex >= 0 {
		return fmt.Sprintf("index:%d", outputIndex)
	}
	return fmt.Sprintf("anon:%p", item)
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
	return jsonutil.StringValue(value)
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

func intValue(value any) (int, bool) {
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
		number, err := typed.Int64()
		return int(number), err == nil
	default:
		return 0, false
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

func firstToolCallState(values ...*ToolCallState) *ToolCallState {
	for _, value := range values {
		if value != nil {
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
