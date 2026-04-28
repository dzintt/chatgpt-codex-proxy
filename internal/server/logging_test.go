package server

import "testing"

func TestFormatPayloadForLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "JSON bytes",
			value: []byte("{\n  \"hello\": \"world\"\n}\n"),
			want:  "{\"hello\":\"world\"}",
		},
		{
			name:  "map value",
			value: map[string]any{"stream": true, "model": "gpt-5.4"},
			want:  "{\"model\":\"gpt-5.4\",\"stream\":true}",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatPayloadForLog(tc.value); got != tc.want {
				t.Fatalf("formatPayloadForLog() = %q, want %q", got, tc.want)
			}
		})
	}
}
