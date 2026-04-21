package server

import "testing"

func TestFormatPayloadForLogCompactsJSONBytes(t *testing.T) {
	t.Parallel()

	got := formatPayloadForLog([]byte("{\n  \"hello\": \"world\"\n}\n"))
	if got != "{\"hello\":\"world\"}" {
		t.Fatalf("formatPayloadForLog() = %q, want compact JSON", got)
	}
}

func TestFormatPayloadForLogMarshalsStructs(t *testing.T) {
	t.Parallel()

	got := formatPayloadForLog(map[string]any{"stream": true, "model": "gpt-5.4"})
	if got != "{\"model\":\"gpt-5.4\",\"stream\":true}" && got != "{\"stream\":true,\"model\":\"gpt-5.4\"}" {
		t.Fatalf("formatPayloadForLog() = %q, want marshaled JSON object", got)
	}
}
