package models

var bootstrapEntries = []Entry{
	{
		ID:                     "gpt-5.4",
		DisplayName:            "gpt-5.4",
		Description:            "Bootstrap fallback model catalog entry",
		IsDefault:              true,
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffort{
			{ReasoningEffort: "low", Description: "Fastest responses"},
			{ReasoningEffort: "medium", Description: "Balanced"},
			{ReasoningEffort: "high", Description: "Greater reasoning depth"},
			{ReasoningEffort: "xhigh", Description: "Extra high reasoning depth"},
		},
		Source: SourceBootstrap,
	},
	{
		ID:                     "gpt-5.4-mini",
		DisplayName:            "gpt-5.4-mini",
		Description:            "Bootstrap fallback model catalog entry",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffort{
			{ReasoningEffort: "low", Description: "Fastest responses"},
			{ReasoningEffort: "medium", Description: "Balanced"},
			{ReasoningEffort: "high", Description: "Greater reasoning depth"},
			{ReasoningEffort: "xhigh", Description: "Extra high reasoning depth"},
		},
		Source: SourceBootstrap,
	},
	{
		ID:                     "gpt-5.3-codex",
		DisplayName:            "gpt-5.3-codex",
		Description:            "Bootstrap fallback model catalog entry",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffort{
			{ReasoningEffort: "low", Description: "Fastest responses"},
			{ReasoningEffort: "medium", Description: "Balanced"},
			{ReasoningEffort: "high", Description: "Greater reasoning depth"},
			{ReasoningEffort: "xhigh", Description: "Extra high reasoning depth"},
		},
		Source: SourceBootstrap,
	},
	{
		ID:                     "gpt-5.2-codex",
		DisplayName:            "gpt-5.2-codex",
		Description:            "Bootstrap fallback model catalog entry",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffort{
			{ReasoningEffort: "low", Description: "Fastest responses"},
			{ReasoningEffort: "medium", Description: "Balanced"},
			{ReasoningEffort: "high", Description: "Greater reasoning depth"},
			{ReasoningEffort: "xhigh", Description: "Extra high reasoning depth"},
		},
		Source: SourceBootstrap,
	},
	{
		ID:                     "gpt-5.2",
		DisplayName:            "gpt-5.2",
		Description:            "Bootstrap fallback model catalog entry",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffort{
			{ReasoningEffort: "low", Description: "Fastest responses"},
			{ReasoningEffort: "medium", Description: "Balanced"},
			{ReasoningEffort: "high", Description: "Greater reasoning depth"},
			{ReasoningEffort: "xhigh", Description: "Extra high reasoning depth"},
		},
		Source: SourceBootstrap,
	},
}

func BootstrapEntries() []Entry {
	out := make([]Entry, 0, len(bootstrapEntries))
	for _, entry := range bootstrapEntries {
		out = append(out, cloneEntry(entry))
	}
	return out
}
