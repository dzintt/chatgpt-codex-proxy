package conversationkey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
)

var leadingSystemReminder = regexp.MustCompile(`(?is)^(?:<system-reminder>[\s\S]*?</system-reminder>\s*)+`)

func Derive(req codex.Request) string {
	instructions, transcript := extractSeed(req)
	model := strings.TrimSpace(req.Model)
	if instructions == "" && transcript == "" {
		return ""
	}

	seed := model + "\x00" + instructions + "\x00" + transcript
	sum := sha256.Sum256([]byte(seed))
	hash := hex.EncodeToString(sum[:])
	return hash[:8] + "-" + hash[8:12] + "-" + hash[12:16] + "-" + hash[16:20] + "-" + hash[20:32]
}

func extractSeed(req codex.Request) (string, string) {
	instructions := strings.TrimSpace(req.Instructions)
	if len(instructions) > 2000 {
		instructions = instructions[:2000]
	}

	items := req.Input
	if prefixEnd := conversationPrefixEnd(items); prefixEnd >= 0 {
		items = items[:prefixEnd+1]
	}

	var transcriptParts []string
	firstUser := true
	for _, item := range items {
		serialized := serializeItem(item, firstUser)
		if serialized == "" {
			continue
		}
		transcriptParts = append(transcriptParts, serialized)
		if item.Role == "user" {
			firstUser = false
		}
	}
	return instructions, strings.Join(transcriptParts, "\x1e")
}

func conversationPrefixEnd(items []codex.InputItem) int {
	lastModelIndex := -1
	for idx, item := range items {
		switch {
		case item.Role == "assistant":
			lastModelIndex = idx
		case item.Type == "function_call", item.Type == "custom_tool_call":
			lastModelIndex = idx
		}
	}
	return lastModelIndex
}

func serializeItem(item codex.InputItem, stripSystemReminder bool) string {
	var fields []string
	if role := strings.TrimSpace(item.Role); role != "" {
		fields = append(fields, "role="+role)
	}
	if itemType := strings.TrimSpace(item.Type); itemType != "" {
		fields = append(fields, "type="+itemType)
	}
	if callID := strings.TrimSpace(item.CallID); callID != "" {
		fields = append(fields, "call_id="+callID)
	}
	if name := strings.TrimSpace(item.Name); name != "" {
		fields = append(fields, "name="+name)
	}
	if input := strings.TrimSpace(item.Input); input != "" {
		fields = append(fields, "input="+input)
	}
	if arguments := strings.TrimSpace(item.Arguments); arguments != "" {
		fields = append(fields, "arguments="+arguments)
	}
	if output := strings.TrimSpace(item.OutputText); output != "" {
		fields = append(fields, "output="+output)
	}
	if text := serializeContent(item.Content, stripSystemReminder); text != "" {
		fields = append(fields, "content="+text)
	}
	if text := serializeContent(item.OutputContent, false); text != "" {
		fields = append(fields, "output_content="+text)
	}
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, "\x1f")
}

func serializeContent(parts []codex.ContentPart, stripSystemReminder bool) string {
	if len(parts) == 0 {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		var value string
		switch part.Type {
		case "", "text", "input_text", "output_text", "reasoning_text":
			value = part.Text
		case "input_image":
			value = fmt.Sprintf("image:%s:%s", strings.TrimSpace(part.ImageURL), strings.TrimSpace(part.Detail))
		case "input_file":
			value = fmt.Sprintf(
				"file:%s:%s:%s:%s:%s",
				strings.TrimSpace(part.FileID),
				strings.TrimSpace(part.Filename),
				strings.TrimSpace(part.FileURL),
				strings.TrimSpace(part.FileData),
				strings.TrimSpace(part.Detail),
			)
		}
		if value == "" {
			continue
		}
		if stripSystemReminder {
			normalized := strings.TrimSpace(leadingSystemReminder.ReplaceAllString(value, ""))
			if normalized != "" {
				value = normalized
			}
			stripSystemReminder = false
		}
		texts = append(texts, value)
	}
	return strings.Join(texts, "\n")
}
