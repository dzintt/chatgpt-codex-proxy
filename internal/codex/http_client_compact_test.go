package codex

import (
	"encoding/json"
	"testing"
)

func TestCompactRequestMarshalOmitsStreamingOnlyFields(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(CompactRequest{
		Model: "gpt-5.4",
		Input: []InputItem{{
			Role:  "assistant",
			Phase: "output",
			Content: []ContentPart{{
				Type: "output_text",
				Text: "compacted output",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := body["stream"]; ok {
		t.Fatalf("stream = %#v, want omitted", body["stream"])
	}
	if _, ok := body["store"]; ok {
		t.Fatalf("store = %#v, want omitted", body["store"])
	}
	if _, ok := body["previous_response_id"]; ok {
		t.Fatalf("previous_response_id = %#v, want omitted", body["previous_response_id"])
	}

	input, _ := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("len(input) = %d, want 1", len(input))
	}
	item, _ := input[0].(map[string]any)
	if got := item["phase"]; got != "output" {
		t.Fatalf("phase = %#v, want output", got)
	}
}

func TestStreamRequestPayloadPreservesServiceTier(t *testing.T) {
	t.Parallel()

	payload := StreamRequestPayload(Request{
		Model:              "gpt-5.4",
		Stream:             false,
		Store:              true,
		PreviousResponseID: "resp_previous",
		ServiceTier:        "priority",
		Input: []InputItem{{
			Role: "user",
			Content: []ContentPart{{
				Type: "input_text",
				Text: "hello",
			}},
		}},
	})

	if !payload.Stream {
		t.Fatal("Stream = false, want true")
	}
	if payload.Store {
		t.Fatal("Store = true, want false")
	}
	if payload.PreviousResponseID != "" {
		t.Fatalf("PreviousResponseID = %q, want empty", payload.PreviousResponseID)
	}
	if payload.ServiceTier != "priority" {
		t.Fatalf("ServiceTier = %q, want priority", payload.ServiceTier)
	}
}

func TestParseCompactResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name:    "valid compaction output",
			payload: `{"output":[{"type":"compaction","id":"cmp_123","encrypted_content":"enc"}]}`,
		},
		{
			name:    "invalid JSON",
			payload: `{"output":`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			response, err := parseCompactResponse(tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatal("parseCompactResponse() error = nil, want JSON decode error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCompactResponse() error = %v", err)
			}
			if len(response.Output) != 1 {
				t.Fatalf("len(output) = %d, want 1", len(response.Output))
			}
			if got := response.Output[0]["type"]; got != "compaction" {
				t.Fatalf("output[0][type] = %#v, want compaction", got)
			}
		})
	}
}
