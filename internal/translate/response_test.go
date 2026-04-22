package translate

import (
	"testing"

	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/jsonutil"
)

func TestResponsesObjectIncludesFunctionCalls(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointResponses,
		Request: codex.Request{
			Model: "gpt-5.4",
		},
	})
	accumulator.ResponseID = "resp_test"
	accumulator.ToolCalls = []*ToolCallState{{
		ItemID:      "fc_123",
		CallID:      "call_123",
		Name:        "ping_tool",
		Arguments:   `{"message":"hello"}`,
		OutputIndex: 0,
		Status:      "completed",
	}}

	response := accumulator.ResponsesObject()
	output, ok := response["output"].([]map[string]any)
	if !ok {
		t.Fatalf("output = %#v", response["output"])
	}
	if len(output) != 1 {
		t.Fatalf("output len = %d, want 1", len(output))
	}
	if output[0]["type"] != "function_call" {
		t.Fatalf("output[0].type = %#v, want function_call", output[0]["type"])
	}
	if output[0]["status"] != "completed" {
		t.Fatalf("output[0].status = %#v, want completed", output[0]["status"])
	}
	if output[0]["id"] != "fc_123" {
		t.Fatalf("output[0].id = %#v, want fc_123", output[0]["id"])
	}
	if output[0]["call_id"] != "call_123" {
		t.Fatalf("output[0].call_id = %#v, want call_123", output[0]["call_id"])
	}
	if output[0]["name"] != "ping_tool" {
		t.Fatalf("output[0].name = %#v, want ping_tool", output[0]["name"])
	}
	if output[0]["arguments"] != `{"message":"hello"}` {
		t.Fatalf("output[0].arguments = %#v", output[0]["arguments"])
	}
}

func TestPatchChatCompletionObjectForTuple(t *testing.T) {
	t.Parallel()

	object := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": `{"pair":{"0":"left","1":2}}`,
				},
			},
		},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pair": map[string]any{
				"type": "array",
				"prefixItems": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "number"},
				},
			},
		},
	}

	if err := PatchChatCompletionObjectForTuple(object, schema); err != nil {
		t.Fatalf("PatchChatCompletionObjectForTuple() error = %v", err)
	}

	choice := jsonutil.SliceOfMaps(object["choices"])[0]
	message, _ := choice["message"].(map[string]any)
	if message["content"] != `{"pair":["left",2]}` {
		t.Fatalf("message.content = %#v, want reconverted tuple JSON", message["content"])
	}
}

func TestPatchResponsesObjectForTuple(t *testing.T) {
	t.Parallel()

	object := map[string]any{
		"output_text": `{"pair":{"0":"left","1":2}}`,
		"output": []any{
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": `{"pair":{"0":"left","1":2}}`,
					},
				},
			},
		},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pair": map[string]any{
				"type": "array",
				"prefixItems": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "number"},
				},
			},
		},
	}

	if err := PatchResponsesObjectForTuple(object, schema); err != nil {
		t.Fatalf("PatchResponsesObjectForTuple() error = %v", err)
	}

	if object["output_text"] != `{"pair":["left",2]}` {
		t.Fatalf("output_text = %#v, want reconverted tuple JSON", object["output_text"])
	}
	content := jsonutil.SliceOfMaps(jsonutil.SliceOfMaps(object["output"])[0]["content"])
	if content[0]["text"] != `{"pair":["left",2]}` {
		t.Fatalf("content[0].text = %#v, want reconverted tuple JSON", content[0]["text"])
	}
}

func TestEnsureResponseToolCallCompletedIncludesCallID(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointResponses,
		Request: codex.Request{
			Model: "gpt-5.4",
		},
	})
	state := &ToolCallState{
		ItemID:      "fc_123",
		CallID:      "call_123",
		Name:        "ping_tool",
		Arguments:   `{"message":"hello"}`,
		OutputIndex: 0,
		Status:      "in_progress",
	}

	events := accumulator.ensureResponseToolCallCompleted(state)
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}

	done := events[1]
	if done.Type != "response.function_call_arguments.done" {
		t.Fatalf("done.Type = %q, want response.function_call_arguments.done", done.Type)
	}
	if done.Payload["call_id"] != "call_123" {
		t.Fatalf("done.Payload[call_id] = %#v, want call_123", done.Payload["call_id"])
	}
}

func TestApplyUpgradesPlaceholderToolCallIDWhenOutputItemProvidesCallID(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointResponses,
		Request: codex.Request{
			Model: "gpt-5.4",
		},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.custom_tool_call_input.delta",
		Raw: map[string]any{
			"response_id":  "resp_custom",
			"item_id":      "ctc_tool",
			"output_index": 0,
			"delta":        "*** Begin Patch\n",
		},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.output_item.added",
		Raw: map[string]any{
			"response_id":  "resp_custom",
			"output_index": 0,
			"item": map[string]any{
				"id":      "ctc_tool",
				"call_id": "call_patch",
				"type":    "custom_tool_call",
				"name":    "ApplyPatch",
				"input":   "*** Begin Patch\n",
				"status":  "in_progress",
			},
		},
	})

	if len(accumulator.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(accumulator.ToolCalls))
	}
	if accumulator.ToolCalls[0].CallID != "call_patch" {
		t.Fatalf("CallID = %q, want call_patch", accumulator.ToolCalls[0].CallID)
	}

	chatObject := accumulator.ChatCompletionObject()
	choices := jsonutil.SliceOfMaps(chatObject["choices"])
	message, _ := choices[0]["message"].(map[string]any)
	toolCalls := jsonutil.SliceOfMaps(message["tool_calls"])
	if toolCalls[0]["id"] != "call_patch" {
		t.Fatalf("tool_calls[0].id = %#v, want call_patch", toolCalls[0]["id"])
	}

	responsesObject := accumulator.ResponsesObject()
	output := responsesObject["output"].([]map[string]any)
	if output[0]["call_id"] != "call_patch" {
		t.Fatalf("output[0].call_id = %#v, want call_patch", output[0]["call_id"])
	}
}

func TestResponsesObjectMergesFunctionCallsWithExistingMessageOutput(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointResponses,
		Request: codex.Request{
			Model: "gpt-5.4",
		},
	})
	accumulator.ResponseID = "resp_merge"
	accumulator.ToolCalls = []*ToolCallState{{
		ItemID:      "fc_123",
		CallID:      "call_123",
		Name:        "ping_tool",
		Arguments:   `{"message":"hello"}`,
		OutputIndex: 0,
		Status:      "completed",
	}}
	accumulator.OutputItems = []*outputItemState{{
		Key:         "id:msg_123",
		OutputIndex: 1,
		Item: map[string]any{
			"id":     "msg_123",
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{{
				"type": "output_text",
				"text": "done",
			}},
		},
	}}

	response := accumulator.ResponsesObject()
	output := response["output"].([]map[string]any)
	if len(output) != 2 {
		t.Fatalf("output len = %d, want 2", len(output))
	}
	if output[0]["type"] != "function_call" {
		t.Fatalf("output[0].type = %#v, want function_call", output[0]["type"])
	}
	if output[1]["type"] != "message" {
		t.Fatalf("output[1].type = %#v, want message", output[1]["type"])
	}
}

func TestResponsesObjectPreservesToolCallOrderByOutputIndex(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointResponses,
		Request: codex.Request{
			Model: "gpt-5.4",
		},
	})
	accumulator.ToolCalls = []*ToolCallState{
		{
			ItemID:      "fc_late",
			CallID:      "call_late",
			Name:        "late_tool",
			Arguments:   `{"value":2}`,
			OutputIndex: 1,
			Status:      "completed",
		},
		{
			ItemID:      "fc_early",
			CallID:      "call_early",
			Name:        "early_tool",
			Arguments:   `{"value":1}`,
			OutputIndex: 0,
			Status:      "completed",
		},
	}

	response := accumulator.ResponsesObject()
	output := response["output"].([]map[string]any)
	if output[0]["call_id"] != "call_early" {
		t.Fatalf("output[0].call_id = %#v, want call_early", output[0]["call_id"])
	}
	if output[1]["call_id"] != "call_late" {
		t.Fatalf("output[1].call_id = %#v, want call_late", output[1]["call_id"])
	}
}

func TestChatCompletionObjectIncludesReasoningContentAndStrictUsage(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointChat,
		Request: codex.Request{
			Model:     "gpt-5.4",
			Reasoning: &codex.Reasoning{Effort: "high"},
		},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.reasoning_summary_text.delta",
		Raw: map[string]any{
			"delta": "Reasoning summary",
		},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.completed",
		Raw: map[string]any{
			"response": map[string]any{
				"id":          "resp_reasoning",
				"model":       "gpt-5.4",
				"status":      "completed",
				"output_text": "Final answer",
				"usage": map[string]any{
					"input_tokens":  12,
					"output_tokens": 7,
					"input_tokens_details": map[string]any{
						"cached_tokens": 5,
					},
					"output_tokens_details": map[string]any{
						"reasoning_tokens": 3,
					},
				},
			},
		},
	})

	response := accumulator.ChatCompletionObject()
	choices, _ := response["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	if message["reasoning_content"] != "Reasoning summary" {
		t.Fatalf("message.reasoning_content = %#v, want reasoning summary", message["reasoning_content"])
	}
	usage, _ := response["usage"].(map[string]any)
	if usage["prompt_tokens"] != int64(12) {
		t.Fatalf("prompt_tokens = %#v, want 12", usage["prompt_tokens"])
	}
	if usage["completion_tokens"] != int64(7) {
		t.Fatalf("completion_tokens = %#v, want 7", usage["completion_tokens"])
	}
	if usage["total_tokens"] != int64(19) {
		t.Fatalf("total_tokens = %#v, want 19", usage["total_tokens"])
	}
	promptDetails, _ := usage["prompt_tokens_details"].(map[string]any)
	if promptDetails["cached_tokens"] != int64(5) {
		t.Fatalf("cached_tokens = %#v, want 5", promptDetails["cached_tokens"])
	}
	completionDetails, _ := usage["completion_tokens_details"].(map[string]any)
	if completionDetails["reasoning_tokens"] != int64(3) {
		t.Fatalf("reasoning_tokens = %#v, want 3", completionDetails["reasoning_tokens"])
	}
}

func TestAccumulatorReasoningSummaryDoesNotUseCompletedOutputFallback(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointChat,
		Request: codex.Request{
			Model:     "gpt-5.4",
			Reasoning: &codex.Reasoning{Effort: "high"},
		},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.completed",
		Raw: map[string]any{
			"response": map[string]any{
				"id":          "resp_reasoning_fallback",
				"model":       "gpt-5.4",
				"status":      "completed",
				"output_text": "Final answer",
				"output": []any{
					map[string]any{
						"type":   "reasoning",
						"id":     "rs_1",
						"status": "completed",
						"summary": []any{
							map[string]any{
								"type": "summary_text",
								"text": "Recovered summary",
							},
						},
					},
					map[string]any{
						"type":   "message",
						"role":   "assistant",
						"status": "completed",
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": "Final answer",
							},
						},
					},
				},
			},
		},
	})

	if summary := accumulator.ReasoningSummary(); summary != "" {
		t.Fatalf("ReasoningSummary() = %q, want empty without streamed reasoning delta", summary)
	}
}

func TestResponsesObjectUsageIncludesDetailFields(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{
		Endpoint: EndpointResponses,
		Request: codex.Request{
			Model: "gpt-5.4",
		},
	})
	accumulator.Apply(&codex.StreamEvent{
		Type: "response.completed",
		Raw: map[string]any{
			"response": map[string]any{
				"id":          "resp_usage",
				"model":       "gpt-5.4",
				"status":      "completed",
				"output_text": "done",
				"usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 4,
					"input_tokens_details": map[string]any{
						"cached_tokens": 2,
					},
					"output_tokens_details": map[string]any{
						"reasoning_tokens": 1,
					},
				},
			},
		},
	})

	response := accumulator.ResponsesObject()
	usage, _ := response["usage"].(map[string]any)
	if usage["input_tokens"] != int64(10) {
		t.Fatalf("input_tokens = %#v, want 10", usage["input_tokens"])
	}
	if usage["output_tokens"] != int64(4) {
		t.Fatalf("output_tokens = %#v, want 4", usage["output_tokens"])
	}
	inputDetails, _ := usage["input_tokens_details"].(map[string]any)
	if inputDetails["cached_tokens"] != int64(2) {
		t.Fatalf("cached_tokens = %#v, want 2", inputDetails["cached_tokens"])
	}
	outputDetails, _ := usage["output_tokens_details"].(map[string]any)
	if outputDetails["reasoning_tokens"] != int64(1) {
		t.Fatalf("reasoning_tokens = %#v, want 1", outputDetails["reasoning_tokens"])
	}
}
