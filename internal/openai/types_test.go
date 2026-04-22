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

func TestToolDefinitionRoundTripPreservesCustomFields(t *testing.T) {
	t.Parallel()

	var tool ToolDefinition
	err := json.Unmarshal([]byte(`{
		"type": "custom",
		"name": "ApplyPatch",
		"description": "Patch a file",
		"format": {
			"type": "grammar",
			"definition": "start: item+"
		},
		"metadata": {
			"origin": "cursor"
		}
	}`), &tool)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if tool.Type != "custom" {
		t.Fatalf("Type = %q, want custom", tool.Type)
	}
	if tool.Name != "ApplyPatch" {
		t.Fatalf("Name = %q, want ApplyPatch", tool.Name)
	}
	if got := tool.Format["type"]; got != "grammar" {
		t.Fatalf("Format[type] = %#v, want grammar", got)
	}
	var metadata map[string]any
	if err := json.Unmarshal(tool.ExtraFields["metadata"], &metadata); err != nil {
		t.Fatalf("metadata unmarshal error = %v", err)
	}
	if got := metadata["origin"]; got != "cursor" {
		t.Fatalf("ExtraFields[metadata][origin] = %#v, want cursor", got)
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("round-trip unmarshal error = %v", err)
	}
	if got := payload["type"]; got != "custom" {
		t.Fatalf("payload[type] = %#v, want custom", got)
	}
	format, _ := payload["format"].(map[string]any)
	if got := format["definition"]; got != "start: item+" {
		t.Fatalf("payload[format][definition] = %#v, want grammar definition", got)
	}
	roundTripMetadata, _ := payload["metadata"].(map[string]any)
	if got := roundTripMetadata["origin"]; got != "cursor" {
		t.Fatalf("payload[metadata][origin] = %#v, want cursor", got)
	}
}
