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
