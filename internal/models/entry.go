package models

type Source string

const (
	SourceBootstrap Source = "bootstrap"
	SourceCache     Source = "cache"
	SourceUpstream  Source = "upstream"
)

type ReasoningEffort struct {
	ReasoningEffort string `json:"reasoning_effort"`
	Description     string `json:"description,omitempty"`
}

type Entry struct {
	ID                        string            `json:"id"`
	DisplayName               string            `json:"display_name"`
	Description               string            `json:"description,omitempty"`
	IsDefault                 bool              `json:"is_default,omitempty"`
	DefaultReasoningEffort    string            `json:"default_reasoning_effort,omitempty"`
	SupportedReasoningEfforts []ReasoningEffort `json:"supported_reasoning_efforts,omitempty"`
	Source                    Source            `json:"source"`
}
