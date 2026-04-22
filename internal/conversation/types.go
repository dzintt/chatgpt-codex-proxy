package conversation

// ContentPart is the shared shape used for persisted continuation content and
// Codex request/response content fragments.
type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FileURL  string `json:"file_url,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// ReasoningPart is the shared text fragment shape for reasoning summaries.
type ReasoningPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
