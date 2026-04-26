package translate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/jsonutil"
	"chatgpt-codex-proxy/internal/models"
	"chatgpt-codex-proxy/internal/openai"
)

const defaultInstructions = "You are a helpful assistant."

type ModelNotFoundError struct {
	Model string
}

func (e *ModelNotFoundError) Error() string {
	return fmt.Sprintf("unsupported model %q", e.Model)
}

type UnsupportedContentPartError struct {
	PartType string
}

func (e *UnsupportedContentPartError) Error() string {
	return fmt.Sprintf("unsupported_content_part: %s", e.PartType)
}

func ChatCompletions(req openai.ChatCompletionsRequest, defaultModel string, catalog ...*models.Catalog) (NormalizedRequest, error) {
	model, modelExplicit, reasoning, serviceTier, err := normalizeModel(req.Model, defaultModel, req.ReasoningEffort, req.ServiceTier, catalog...)
	if err != nil {
		return NormalizedRequest{}, err
	}
	tools := req.Tools
	if len(tools) == 0 && len(req.Functions) > 0 {
		tools = legacyFunctionsAsTools(req.Functions)
	}
	toolChoice := json.RawMessage(nil)
	if len(req.Tools) > 0 {
		toolChoice = normalizeToolChoice(req.ToolChoice)
	} else if choice := normalizeLegacyFunctionChoice(req.FunctionCall); choice != nil {
		toolChoice = choice
	}
	out := newNormalizedRequest(
		EndpointChat,
		model,
		modelExplicit,
		collectChatCompatibilityWarnings(req),
		req.Stream,
		normalizeTools(tools),
		toolChoice,
		reasoning,
		serviceTier,
		req.PreviousResponseID,
	)
	if req.ResponseFormat != nil {
		text, tupleSchema, err := normalizeChatResponseFormat(req.ResponseFormat)
		if err != nil {
			return NormalizedRequest{}, err
		}
		out.Text = text
		out.TupleSchema = tupleSchema
	}

	var instructions []string
	customToolNames := chatCustomToolNames(req.Tools)
	toolCallTypes := make(map[string]string)
	for _, message := range req.Messages {
		if err := normalizeChatMessage(&out, &instructions, toolCallTypes, customToolNames, message); err != nil {
			return NormalizedRequest{}, err
		}
	}

	if len(out.Input) == 0 {
		out.Input = append(out.Input, codex.InputItem{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: "",
			}},
		})
	}
	out.Instructions = jsonutil.FirstNonEmpty(strings.TrimSpace(strings.Join(instructions, "\n\n")), defaultInstructions)
	return out, nil
}

func chatCustomToolNames(tools []openai.ToolDefinition) map[string]bool {
	if len(tools) == 0 {
		return nil
	}
	names := make(map[string]bool)
	for _, tool := range tools {
		if strings.TrimSpace(tool.Type) != "custom" {
			continue
		}
		if name := strings.TrimSpace(tool.Name); name != "" {
			names[name] = true
		}
	}
	return names
}

func customToolInputFromFunctionArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}
	var payload struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err == nil && payload.Input != "" {
		return payload.Input
	}
	return arguments
}

func Responses(req openai.ResponsesRequest, defaultModel string, catalog ...*models.Catalog) (NormalizedRequest, error) {
	model, modelExplicit, reasoning, serviceTier, err := normalizeModel(req.Model, defaultModel, "", req.ServiceTier, catalog...)
	if err != nil {
		return NormalizedRequest{}, err
	}
	if req.Reasoning != nil {
		explicit := &codex.Reasoning{
			Effort:  req.Reasoning.Effort,
			Summary: req.Reasoning.Summary,
		}
		if explicit.Effort != "" && explicit.Summary == "" {
			explicit.Summary = "auto"
		}
		reasoning = explicit
	}

	out := newNormalizedRequest(
		EndpointResponses,
		model,
		modelExplicit,
		collectResponsesCompatibilityWarnings(req),
		req.Stream,
		normalizeTools(req.Tools),
		normalizeToolChoice(req.ToolChoice),
		reasoning,
		serviceTier,
		req.PreviousResponseID,
	)
	var instructions []string
	if text := strings.TrimSpace(req.Instructions); text != "" {
		instructions = append(instructions, text)
	}

	out.Text, out.TupleSchema = normalizeResponsesText(req.Text)

	if req.Input.String != "" {
		out.Input = append(out.Input, codex.InputItem{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: req.Input.String,
			}},
		})
	}

	for _, item := range req.Input.Items {
		if err := normalizeResponsesInputItem(&out, &instructions, item); err != nil {
			return NormalizedRequest{}, err
		}
	}

	out.Instructions = jsonutil.FirstNonEmpty(strings.TrimSpace(strings.Join(instructions, "\n\n")), defaultInstructions)
	return out, nil
}

func Compact(req openai.ResponsesCompactRequest, defaultModel string, catalog ...*models.Catalog) (NormalizedCompactRequest, error) {
	model, modelExplicit, reasoning, _, err := normalizeModel(req.Model, defaultModel, "", "", catalog...)
	if err != nil {
		return NormalizedCompactRequest{}, err
	}
	if req.Reasoning != nil {
		explicit := &codex.Reasoning{
			Effort:  req.Reasoning.Effort,
			Summary: req.Reasoning.Summary,
		}
		if explicit.Effort != "" && explicit.Summary == "" {
			explicit.Summary = "auto"
		}
		reasoning = explicit
	}

	out := NormalizedCompactRequest{
		ModelExplicit:         modelExplicit,
		PreviousResponseID:    strings.TrimSpace(req.PreviousResponseID),
		CompatibilityWarnings: collectCompactCompatibilityWarnings(req),
		CompactRequest: codex.CompactRequest{
			Model:     model,
			Reasoning: reasoning,
		},
	}
	var instructions []string
	if text := strings.TrimSpace(req.Instructions); text != "" {
		instructions = append(instructions, text)
	}

	out.Text, out.TupleSchema = normalizeResponsesText(req.Text)

	if req.Input.String != "" {
		out.Input = append(out.Input, codex.InputItem{
			Role: "user",
			Content: []codex.ContentPart{{
				Type: "input_text",
				Text: req.Input.String,
			}},
		})
	}

	for _, item := range req.Input.Items {
		if err := appendResponsesInputItem(&out.Input, &instructions, item); err != nil {
			return NormalizedCompactRequest{}, err
		}
	}

	out.Instructions = jsonutil.FirstNonEmpty(strings.TrimSpace(strings.Join(instructions, "\n\n")), defaultInstructions)
	return out, nil
}

func newNormalizedRequest(endpoint Endpoint, model string, modelExplicit bool, warnings []CompatibilityWarning, stream bool, tools []codex.Tool, toolChoice json.RawMessage, reasoning *codex.Reasoning, serviceTier, previousResponseID string) NormalizedRequest {
	return NormalizedRequest{
		Endpoint:              endpoint,
		ModelExplicit:         modelExplicit,
		CompatibilityWarnings: warnings,
		Request: codex.Request{
			Model:              model,
			Stream:             stream,
			Tools:              tools,
			ToolChoice:         toolChoice,
			Reasoning:          reasoning,
			ServiceTier:        serviceTier,
			PreviousResponseID: strings.TrimSpace(previousResponseID),
			Include:            reasoningInclude(reasoning),
		},
	}
}

func normalizeChatMessage(out *NormalizedRequest, instructions *[]string, toolCallTypes map[string]string, customToolNames map[string]bool, message openai.ChatMessage) error {
	switch message.Role {
	case "system", "developer":
		return appendInstructionText(instructions, message.Content)
	case "user", "assistant":
		if len(message.ToolCalls) > 0 {
			if err := appendRoleContentInputIfPresent(out, message.Role, message.Content); err != nil {
				return err
			}
			for _, call := range message.ToolCalls {
				callType := normalizeChatToolCallType(call, customToolNames)
				toolCallTypes[call.ID] = callType
				out.Input = append(out.Input, chatToolCallInputItem(call, callType))
			}
			return nil
		}
		if message.FunctionCall != nil {
			out.Input = append(out.Input, codex.InputItem{
				Type:      "function_call",
				Name:      message.FunctionCall.Name,
				Arguments: message.FunctionCall.Arguments,
			})
			return nil
		}
		return appendRoleContentInput(&out.Input, message.Role, "", message.Content)
	case "tool":
		text, err := flattenContent(message.Content)
		if err != nil {
			return err
		}
		itemType := "function_call_output"
		if toolCallTypes[message.ToolCallID] == "custom" {
			itemType = "custom_tool_call_output"
		}
		out.Input = append(out.Input, codex.InputItem{
			Type:       itemType,
			CallID:     message.ToolCallID,
			OutputText: text,
		})
		return nil
	default:
		return fmt.Errorf("unsupported role %q", message.Role)
	}
}

func normalizeChatToolCallType(call openai.ToolCall, customToolNames map[string]bool) string {
	callType := strings.TrimSpace(call.Type)
	if callType == "" {
		callType = "function"
	}
	if callType == "function" && customToolNames[strings.TrimSpace(call.Function.Name)] {
		return "custom"
	}
	return callType
}

func chatToolCallInputItem(call openai.ToolCall, callType string) codex.InputItem {
	if callType != "custom" {
		return codex.InputItem{
			Type:      "function_call",
			CallID:    call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		}
	}

	name := ""
	input := ""
	if call.Custom != nil {
		name = call.Custom.Name
		input = call.Custom.Input
	}
	if name == "" {
		name = call.Function.Name
	}
	if input == "" {
		input = customToolInputFromFunctionArguments(call.Function.Arguments)
	}
	return codex.InputItem{
		Type:   "custom_tool_call",
		CallID: call.ID,
		Name:   name,
		Input:  input,
	}
}

func normalizeResponsesText(text *openai.ResponsesText) (*codex.TextConfig, map[string]any) {
	if text == nil || text.Format == nil {
		return nil, nil
	}

	var tupleSchema map[string]any
	format := codex.TextFormat{
		Type: text.Format.Type,
		Name: text.Format.Name,
	}
	if text.Format.Type == "json_schema" {
		prepared, original := PrepareSchema(text.Format.Schema)
		format.Schema = prepared
		format.Strict = text.Format.Strict
		tupleSchema = original
	} else {
		format.Schema = text.Format.Schema
		format.Strict = text.Format.Strict
	}
	return &codex.TextConfig{Format: format}, tupleSchema
}

func normalizeResponsesInputItem(out *NormalizedRequest, instructions *[]string, item openai.ResponsesInputItem) error {
	return appendResponsesInputItem(&out.Input, instructions, item)
}

func appendResponsesInputItem(out *[]codex.InputItem, instructions *[]string, item openai.ResponsesInputItem) error {
	if item.Type == "" && (item.Role == "system" || item.Role == "developer") {
		return appendInstructionText(instructions, item.Content)
	}

	switch item.Type {
	case "message":
		return appendReplayMessageInput(out, item)
	case "web_search_call":
		*out = append(*out, codex.InputItem{
			Type:   "web_search_call",
			ID:     strings.TrimSpace(item.ID),
			Status: strings.TrimSpace(item.Status),
		})
	case "function_call":
		*out = append(*out, codex.InputItem{
			Type:      "function_call",
			CallID:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
		})
	case "custom_tool_call":
		*out = append(*out, codex.InputItem{
			Type:   "custom_tool_call",
			CallID: item.CallID,
			Name:   item.Name,
			Input:  item.Input,
		})
	case "function_call_output", "custom_tool_call_output":
		output, err := normalizeOutputItem(item.Type, item.CallID, item.OutputText, item.OutputContent)
		if err != nil {
			return err
		}
		*out = append(*out, output)
	case "reasoning":
		parts, err := normalizeContentPartsChecked(item.Content)
		if err != nil {
			return err
		}
		*out = append(*out, codex.InputItem{
			Type:             "reasoning",
			ID:               strings.TrimSpace(item.ID),
			Status:           strings.TrimSpace(item.Status),
			Content:          parts,
			Summary:          append([]openai.ReasoningPart(nil), item.Summary...),
			EncryptedContent: strings.TrimSpace(item.EncryptedContent),
		})
	case "compaction":
		*out = append(*out, codex.InputItem{
			Type:             "compaction",
			ID:               strings.TrimSpace(item.ID),
			EncryptedContent: strings.TrimSpace(item.EncryptedContent),
		})
	default:
		role := item.Role
		if role == "" {
			role = "user"
		}
		return appendRoleContentInput(out, role, item.Phase, item.Content)
	}
	return nil
}

func appendReplayMessageInput(out *[]codex.InputItem, item openai.ResponsesInputItem) error {
	role := item.Role
	if role == "" {
		role = "user"
	}
	phase := replayMessagePhase(role, item.Phase)
	parts, err := normalizeContentPartsChecked(item.Content)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		parts = []codex.ContentPart{{
			Type: replayMessageTextPartType(role, phase),
			Text: "",
		}}
	}
	*out = append(*out, codex.InputItem{
		Role:    role,
		Phase:   phase,
		Content: parts,
	})
	return nil
}

func replayMessagePhase(role, phase string) string {
	trimmedPhase := strings.TrimSpace(phase)
	if trimmedPhase != "" {
		return trimmedPhase
	}
	if strings.TrimSpace(role) == "assistant" {
		return "output"
	}
	return ""
}

func replayMessageTextPartType(role, phase string) string {
	if strings.TrimSpace(phase) == "output" || strings.TrimSpace(role) == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func appendInstructionText(instructions *[]string, content openai.MessageContent) error {
	text, err := flattenContent(content)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) != "" {
		*instructions = append(*instructions, text)
	}
	return nil
}

func appendRoleContentInput(out *[]codex.InputItem, role, phase string, content openai.MessageContent) error {
	parts, err := normalizeContentPartsChecked(content)
	if err != nil {
		return err
	}
	*out = append(*out, codex.InputItem{
		Role:    role,
		Phase:   strings.TrimSpace(phase),
		Content: parts,
	})
	return nil
}

func appendRoleContentInputIfPresent(out *NormalizedRequest, role string, content openai.MessageContent) error {
	parts, err := normalizeContentPartsChecked(content)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return nil
	}
	out.Input = append(out.Input, codex.InputItem{
		Role:    role,
		Content: parts,
	})
	return nil
}

func normalizeOutputItem(itemType, callID, outputText string, outputContent openai.MessageContent) (codex.InputItem, error) {
	parts, err := normalizeContentPartsChecked(outputContent)
	if err != nil {
		return codex.InputItem{}, err
	}
	return codex.InputItem{
		Type:          itemType,
		CallID:        callID,
		OutputText:    outputText,
		OutputContent: parts,
	}, nil
}

func legacyFunctionsAsTools(functions []openai.LegacyFunctionDefinition) []openai.ToolDefinition {
	if len(functions) == 0 {
		return nil
	}
	tools := make([]openai.ToolDefinition, 0, len(functions))
	for _, function := range functions {
		tools = append(tools, openai.ToolDefinition{
			Type: "function",
			Function: &openai.FunctionTool{
				Name:        function.Name,
				Description: function.Description,
				Parameters:  function.Parameters,
			},
		})
	}
	return tools
}

func normalizeTools(tools []openai.ToolDefinition) []codex.Tool {
	if len(tools) == 0 {
		return nil
	}

	result := make([]codex.Tool, 0, len(tools))
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			function := tool.Function
			if function == nil && strings.TrimSpace(tool.Name) != "" {
				function = &openai.FunctionTool{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
					Strict:      tool.Strict,
				}
			}
			if function == nil {
				continue
			}
			result = append(result, codex.Tool{
				Type:        "function",
				Name:        function.Name,
				Description: function.Description,
				Parameters:  NormalizeSchema(function.Parameters),
				Strict:      function.Strict,
			})
		case "web_search", "web_search_preview":
			result = append(result, codex.Tool{
				Type:              "web_search",
				SearchContextSize: tool.SearchContextSize,
				UserLocation:      tool.UserLocation,
			})
		default:
			result = append(result, tool)
		}
	}
	return result
}

func normalizeToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		return mustJSONString(mode)
	}
	var choice struct {
		Type     string `json:"type"`
		Name     string `json:"name,omitempty"`
		Function *struct {
			Name string `json:"name"`
		} `json:"function,omitempty"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	switch strings.TrimSpace(choice.Type) {
	case "function":
		name := strings.TrimSpace(choice.Name)
		if name == "" && choice.Function != nil {
			name = strings.TrimSpace(choice.Function.Name)
		}
		if name != "" {
			return functionToolChoiceJSON(name)
		}
	case "web_search", "web_search_preview":
		return webSearchToolChoiceJSON()
	}
	return append(json.RawMessage(nil), raw...)
}

func normalizeLegacyFunctionChoice(choice *openai.LegacyFunctionCallChoice) json.RawMessage {
	if choice == nil || choice.IsZero() {
		return nil
	}
	switch strings.TrimSpace(choice.Mode) {
	case "none", "auto":
		return mustJSONString(choice.Mode)
	}
	if name := strings.TrimSpace(choice.Name); name != "" {
		return functionToolChoiceJSON(name)
	}
	return nil
}

func normalizeChatResponseFormat(format *openai.ResponseFormat) (*codex.TextConfig, map[string]any, error) {
	if format == nil {
		return nil, nil, nil
	}
	switch format.Type {
	case "", "text":
		return nil, nil, nil
	case "json_object":
		return &codex.TextConfig{
			Format: codex.TextFormat{Type: "json_object"},
		}, nil, nil
	case "json_schema":
		if format.JSONSchema == nil {
			return nil, nil, fmt.Errorf("response_format.json_schema is required")
		}
		prepared, tupleSchema := PrepareSchema(format.JSONSchema.Schema)
		return &codex.TextConfig{
			Format: codex.TextFormat{
				Type:   "json_schema",
				Name:   format.JSONSchema.Name,
				Schema: prepared,
				Strict: format.JSONSchema.Strict,
			},
		}, tupleSchema, nil
	default:
		return nil, nil, fmt.Errorf("unsupported response_format %q", format.Type)
	}
}

func reasoningInclude(reasoning *codex.Reasoning) []string {
	if reasoning == nil {
		return nil
	}
	return []string{"reasoning.encrypted_content"}
}

func normalizeContentPartsChecked(parts openai.MessageContent) ([]codex.ContentPart, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]codex.ContentPart, 0, len(parts))
	for _, part := range parts {
		contentType, kind, ok := classifyContentPartType(part.Type)
		if !ok {
			return nil, unsupportedContentPartError(part.Type)
		}
		switch kind {
		case contentPartText:
			out = append(out, codex.ContentPart{
				Type: contentType,
				Text: part.Text,
			})
		case contentPartImage:
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return nil, fmt.Errorf("image_url.url is required")
			}
			out = append(out, codex.ContentPart{
				Type:     "input_image",
				ImageURL: strings.TrimSpace(part.ImageURL.URL),
				Detail:   strings.TrimSpace(part.Detail),
			})
		case contentPartFile:
			if strings.TrimSpace(part.FileData) == "" && strings.TrimSpace(part.FileURL) == "" && strings.TrimSpace(part.FileID) == "" {
				return nil, fmt.Errorf("input_file requires file_data, file_url, or file_id")
			}
			out = append(out, codex.ContentPart{
				Type:     "input_file",
				Detail:   strings.TrimSpace(part.Detail),
				FileURL:  strings.TrimSpace(part.FileURL),
				FileData: strings.TrimSpace(part.FileData),
				FileID:   strings.TrimSpace(part.FileID),
				Filename: strings.TrimSpace(part.Filename),
			})
		}
	}
	return out, nil
}

func mustJSONString(value string) json.RawMessage {
	return json.RawMessage(strconv.Quote(value))
}

func functionToolChoiceJSON(name string) json.RawMessage {
	return json.RawMessage(`{"type":"function","name":` + strconv.Quote(name) + `}`)
}

func webSearchToolChoiceJSON() json.RawMessage {
	return json.RawMessage(`{"type":"web_search"}`)
}

func flattenContent(content openai.MessageContent) (string, error) {
	var parts []string
	for _, part := range content {
		_, kind, ok := classifyContentPartType(part.Type)
		if !ok || kind != contentPartText {
			return "", unsupportedContentPartError(part.Type)
		}
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

type contentPartKind uint8

const (
	contentPartText contentPartKind = iota
	contentPartImage
	contentPartFile
)

func classifyContentPartType(partType string) (string, contentPartKind, bool) {
	switch partType {
	case "", "text", "input_text":
		return "input_text", contentPartText, true
	case "output_text":
		return "output_text", contentPartText, true
	case "reasoning_text":
		return "reasoning_text", contentPartText, true
	case "image_url", "input_image":
		return "input_image", contentPartImage, true
	case "input_file":
		return "input_file", contentPartFile, true
	default:
		return "", 0, false
	}
}

func unsupportedContentPartError(partType string) error {
	return &UnsupportedContentPartError{PartType: partType}
}

func normalizeModel(rawModel, defaultModel, reasoningEffort, serviceTier string, catalogs ...*models.Catalog) (string, bool, *codex.Reasoning, string, error) {
	catalog := firstCatalog(catalogs...)
	model := strings.TrimSpace(rawModel)
	modelExplicit := model != ""
	if modelExplicit {
		if catalog != nil {
			if !catalog.Has(model) {
				return "", false, nil, "", &ModelNotFoundError{Model: model}
			}
		} else if !bootstrapModelSupported(model) {
			return "", false, nil, "", &ModelNotFoundError{Model: model}
		}
	}

	effort := strings.TrimSpace(reasoningEffort)
	var reasoning *codex.Reasoning
	if effort != "" {
		reasoning = &codex.Reasoning{Effort: effort, Summary: "auto"}
	}
	return model, modelExplicit, reasoning, normalizeServiceTier(serviceTier), nil
}

func normalizeServiceTier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast":
		return "priority"
	default:
		return strings.TrimSpace(value)
	}
}

func firstCatalog(catalogs ...*models.Catalog) *models.Catalog {
	for _, catalog := range catalogs {
		if catalog != nil {
			return catalog
		}
	}
	return nil
}

func bootstrapModelSupported(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, entry := range models.BootstrapEntries() {
		if entry.ID == model {
			return true
		}
	}
	return false
}

func collectChatCompatibilityWarnings(req openai.ChatCompletionsRequest) []CompatibilityWarning {
	var warnings []CompatibilityWarning
	if req.N != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "n"))
	}
	if req.Temperature != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "temperature"))
	}
	if req.TopP != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "top_p"))
	}
	if req.MaxTokens != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "max_tokens"))
	}
	if req.PresencePenalty != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "presence_penalty"))
	}
	if req.FrequencyPenalty != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "frequency_penalty"))
	}
	if len(req.Stop) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "stop"))
	}
	if req.User != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "user"))
	}
	if req.ParallelToolCalls != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "parallel_tool_calls"))
	}
	if len(req.StreamOptions) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointChat, "stream_options"))
	}
	return warnings
}

func collectResponsesCompatibilityWarnings(req openai.ResponsesRequest) []CompatibilityWarning {
	var warnings []CompatibilityWarning
	if req.Temperature != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "temperature"))
	}
	if req.TopP != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "top_p"))
	}
	if req.MaxOutputTokens != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "max_output_tokens"))
	}
	if req.ParallelToolCalls != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "parallel_tool_calls"))
	}
	if req.Store != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "store"))
	}
	if req.Background != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "background"))
	}
	if req.User != nil {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "user"))
	}
	if len(req.Metadata) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "metadata"))
	}
	if len(req.StreamOptions) > 0 {
		warnings = append(warnings, unsupportedFieldWarning(EndpointResponses, "stream_options"))
	}
	return warnings
}

func collectCompactCompatibilityWarnings(req openai.ResponsesCompactRequest) []CompatibilityWarning {
	_ = req
	return nil
}

func unsupportedFieldWarning(endpoint Endpoint, field string) CompatibilityWarning {
	return CompatibilityWarning{
		Field:    field,
		Endpoint: endpoint,
		Behavior: "ignored_with_warning",
		Detail:   "field is accepted for compatibility but not applied in this proxy",
	}
}
