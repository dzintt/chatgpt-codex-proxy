package translate

import "testing"

func TestResponsesObjectIncludesFunctionCalls(t *testing.T) {
	t.Parallel()

	accumulator := NewAccumulator(NormalizedRequest{Endpoint: EndpointResponses, Model: "gpt-5.4"})
	accumulator.ResponseID = "resp_test"
	accumulator.ToolCalls = []map[string]any{{
		"id": "call_123",
		"function": map[string]any{
			"name":      "ping_tool",
			"arguments": `{"message":"hello"}`,
		},
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

	choice := sliceOfMaps(object["choices"])[0]
	message := nestedMap(choice, "message")
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
	content := sliceOfMaps(sliceOfMaps(object["output"])[0]["content"])
	if content[0]["text"] != `{"pair":["left",2]}` {
		t.Fatalf("content[0].text = %#v, want reconverted tuple JSON", content[0]["text"])
	}
}

func TestPatchResponseCompletedPayloadForTuple(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"response": map[string]any{
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

	if err := PatchResponseCompletedPayloadForTuple(payload, schema); err != nil {
		t.Fatalf("PatchResponseCompletedPayloadForTuple() error = %v", err)
	}

	response := nestedMap(payload, "response")
	if response["output_text"] != `{"pair":["left",2]}` {
		t.Fatalf("response.output_text = %#v", response["output_text"])
	}
	content := sliceOfMaps(sliceOfMaps(response["output"])[0]["content"])
	if content[0]["text"] != `{"pair":["left",2]}` {
		t.Fatalf("content[0].text = %#v", content[0]["text"])
	}
}
