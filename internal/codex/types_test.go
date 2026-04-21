package codex

import (
	"encoding/json"
	"testing"
)

func TestInputItemMarshalJSONUsesStringForTextOnlyRoleMessages(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(InputItem{
		Role: "user",
		Content: []ContentPart{
			{Type: "input_text", Text: "first line"},
			{Type: "input_text", Text: "second line"},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded["content"] != "first line\nsecond line" {
		t.Fatalf("content = %#v, want joined string", decoded["content"])
	}
}

func TestInputItemMarshalJSONPreservesStructuredUserContent(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(InputItem{
		Role: "user",
		Content: []ContentPart{
			{Type: "input_text", Text: "look at this"},
			{Type: "input_image", ImageURL: "https://example.com/image.png"},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	content, ok := decoded["content"].([]any)
	if !ok {
		t.Fatalf("content = %#v, want structured array", decoded["content"])
	}
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}
}

func TestInputItemMarshalJSONKeepsEmptyStringContentForRoleMessages(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(InputItem{
		Role: "user",
		Content: []ContentPart{{
			Type: "input_text",
			Text: "",
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	value, ok := decoded["content"]
	if !ok {
		t.Fatalf("content missing from payload = %#v", decoded)
	}
	if value != "" {
		t.Fatalf("content = %#v, want empty string", value)
	}
}

func TestInputItemMarshalJSONPreservesStructuredToolOutput(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(InputItem{
		Type:   "function_call_output",
		CallID: "call_1",
		OutputContent: []ContentPart{
			{Type: "input_text", Text: "Result of search"},
			{Type: "input_text", Text: "README.md"},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	output, ok := decoded["output"].([]any)
	if !ok {
		t.Fatalf("output = %#v, want structured array", decoded["output"])
	}
	if len(output) != 2 {
		t.Fatalf("output len = %d, want 2", len(output))
	}
}
