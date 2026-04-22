package conversationkey

import (
	"testing"

	"chatgpt-codex-proxy/internal/codex"
)

func TestDeriveIgnoresLeadingSystemReminderBlocks(t *testing.T) {
	t.Parallel()

	base := codex.Request{
		Model:        "gpt-5.4",
		Instructions: "Be concise.",
		Input: []codex.InputItem{{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: "hello world",
			}},
		}},
	}
	withReminder := base
	withReminder.Input = []codex.InputItem{{
		Role: "user",
		Content: []codex.ContentPart{{
			Type: "input_text",
			Text: "<system-reminder>internal</system-reminder>\nhello world",
		}},
	}}

	if Derive(base) != Derive(withReminder) {
		t.Fatal("Derive() changed when only a leading system-reminder block was added")
	}
}

func TestDeriveChangesForDifferentFirstUserText(t *testing.T) {
	t.Parallel()

	first := codex.Request{
		Model:        "gpt-5.4",
		Instructions: "Be concise.",
		Input: []codex.InputItem{{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: "hello world",
			}},
		}},
	}
	second := first
	second.Input = []codex.InputItem{{
		Role: "user",
		Content: []codex.ContentPart{{
			Type: "input_text",
			Text: "different text",
		}},
	}}

	if Derive(first) == Derive(second) {
		t.Fatal("Derive() stayed the same for different first user text")
	}
}

func TestDeriveChangesForDifferentEstablishedConversationHistory(t *testing.T) {
	t.Parallel()

	first := codex.Request{
		Model:        "gpt-5.4",
		Instructions: "Be concise.",
		Input: []codex.InputItem{
			{
				Role: "user",
				Content: []codex.ContentPart{{
					Type: "input_text",
					Text: "hello world",
				}},
			},
			{
				Role: "assistant",
				Content: []codex.ContentPart{{
					Type: "output_text",
					Text: "assistant one",
				}},
			},
			{
				Role: "user",
				Content: []codex.ContentPart{{
					Type: "input_text",
					Text: "follow up",
				}},
			},
		},
	}
	second := first
	second.Input = []codex.InputItem{
		first.Input[0],
		{
			Role: "assistant",
			Content: []codex.ContentPart{{
				Type: "output_text",
				Text: "assistant two",
			}},
		},
		first.Input[2],
	}

	if Derive(first) == Derive(second) {
		t.Fatal("Derive() stayed the same for different established assistant history")
	}
}
