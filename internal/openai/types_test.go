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
