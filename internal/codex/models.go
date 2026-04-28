package codex

type BackendReasoningEffort struct {
	ReasoningEffort    string `json:"reasoning_effort,omitempty"`
	ReasoningEffortAlt string `json:"reasoningEffort,omitempty"`
	Effort             string `json:"effort,omitempty"`
	Description        string `json:"description,omitempty"`
}

type BackendReasoningLevel struct {
	Effort      string `json:"effort,omitempty"`
	Description string `json:"description,omitempty"`
}

type BackendModelEntry struct {
	Slug                      string                   `json:"slug,omitempty"`
	ID                        string                   `json:"id,omitempty"`
	Name                      string                   `json:"name,omitempty"`
	DisplayName               string                   `json:"display_name,omitempty"`
	Description               string                   `json:"description,omitempty"`
	IsDefault                 bool                     `json:"is_default,omitempty"`
	DefaultReasoningEffort    string                   `json:"default_reasoning_effort,omitempty"`
	DefaultReasoningLevel     string                   `json:"default_reasoning_level,omitempty"`
	SupportedReasoningEfforts []BackendReasoningEffort `json:"supported_reasoning_efforts,omitempty"`
	SupportedReasoningLevels  []BackendReasoningLevel  `json:"supported_reasoning_levels,omitempty"`
}
