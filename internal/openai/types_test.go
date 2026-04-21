package openai

import (
	"encoding/json"
	"testing"
)

func TestResponsesInputUnmarshalAcceptsSingleObject(t *testing.T) {
	t.Parallel()

	var input ResponsesInput
	err := json.Unmarshal([]byte(`{
		"role": "user",
		"content": [{"type": "text", "text": "hello"}]
	}`), &input)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if input.String != "" {
		t.Fatalf("String = %q, want empty", input.String)
	}
	if len(input.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(input.Items))
	}
	if input.Items[0].Role != "user" {
		t.Fatalf("Role = %q, want user", input.Items[0].Role)
	}
}

func TestResponsesInputItemUnmarshalFlattensArrayOutput(t *testing.T) {
	t.Parallel()

	var item ResponsesInputItem
	err := json.Unmarshal([]byte(`{
		"type": "function_call_output",
		"call_id": "call_123",
		"output": [
			{"type": "output_text", "text": "line 1"},
			{"type": "output_text", "text": "line 2"}
		]
	}`), &item)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if item.Type != "function_call_output" {
		t.Fatalf("Type = %q, want function_call_output", item.Type)
	}
	if item.OutputText != "" {
		t.Fatalf("OutputText = %q, want empty", item.OutputText)
	}
	if len(item.OutputContent) != 2 {
		t.Fatalf("len(OutputContent) = %d, want 2", len(item.OutputContent))
	}
	if item.OutputContent[0].Text != "line 1" || item.OutputContent[1].Text != "line 2" {
		t.Fatalf("OutputContent = %#v, want preserved output_text parts", item.OutputContent)
	}
}

func TestResponsesInputItemUnmarshalNormalizesObjectOutput(t *testing.T) {
	t.Parallel()

	var item ResponsesInputItem
	err := json.Unmarshal([]byte(`{
		"type": "function_call_output",
		"call_id": "call_456",
		"output": {"ok": true, "count": 2}
	}`), &item)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if item.OutputText != `{"count":2,"ok":true}` {
		t.Fatalf("OutputText = %q, want normalized JSON string", item.OutputText)
	}
	if len(item.OutputContent) != 0 {
		t.Fatalf("OutputContent = %#v, want empty", item.OutputContent)
	}
}
