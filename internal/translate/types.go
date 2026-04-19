package translate

import "chatgpt-codex-proxy/internal/codex"

type Endpoint string

const (
	EndpointChat      Endpoint = "chat_completions"
	EndpointResponses Endpoint = "responses"
)

type NormalizedRequest struct {
	Endpoint           Endpoint
	Model              string
	Instructions       string
	Input              []codex.InputItem
	Stream             bool
	Tools              []codex.Tool
	ToolChoice         any
	Text               *codex.TextConfig
	Reasoning          *codex.Reasoning
	ServiceTier        string
	PreviousResponseID string
}

func (n NormalizedRequest) ToCodexRequest() codex.Request {
	return codex.Request{
		Model:              n.Model,
		Instructions:       n.Instructions,
		Input:              n.Input,
		Stream:             n.Stream,
		Store:              false,
		Tools:              n.Tools,
		ToolChoice:         n.ToolChoice,
		Text:               n.Text,
		Reasoning:          n.Reasoning,
		ServiceTier:        n.ServiceTier,
		PreviousResponseID: n.PreviousResponseID,
	}
}
