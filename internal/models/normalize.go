package models

import (
	"sort"
	"strings"

	"chatgpt-codex-proxy/internal/codex"
)

func NormalizeBackendEntries(entries []codex.BackendModelEntry) []Entry {
	modelsByID := make(map[string]Entry)
	order := make([]string, 0, len(entries))

	for _, raw := range entries {
		entry, ok := normalizeBackendEntry(raw)
		if !ok {
			continue
		}
		if _, exists := modelsByID[entry.ID]; !exists {
			order = append(order, entry.ID)
		}
		modelsByID[entry.ID] = entry
	}

	normalized := make([]Entry, 0, len(modelsByID))
	for _, id := range order {
		normalized = append(normalized, modelsByID[id])
	}
	if len(order) == 0 {
		keys := make([]string, 0, len(modelsByID))
		for id := range modelsByID {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			normalized = append(normalized, modelsByID[id])
		}
	}
	return normalized
}

func normalizeBackendEntry(raw codex.BackendModelEntry) (Entry, bool) {
	id := strings.TrimSpace(firstNonEmpty(raw.Slug, raw.ID, raw.Name))
	if id == "" {
		return Entry{}, false
	}

	efforts := normalizeEfforts(raw)
	defaultEffort := strings.TrimSpace(firstNonEmpty(raw.DefaultReasoningEffort, raw.DefaultReasoningLevel))
	if defaultEffort == "" && len(efforts) > 0 {
		defaultEffort = efforts[0].ReasoningEffort
	}
	if defaultEffort == "" {
		defaultEffort = "medium"
	}

	return Entry{
		ID:                        id,
		DisplayName:               firstNonEmpty(strings.TrimSpace(raw.DisplayName), strings.TrimSpace(raw.Name), id),
		Description:               strings.TrimSpace(raw.Description),
		IsDefault:                 raw.IsDefault,
		DefaultReasoningEffort:    defaultEffort,
		SupportedReasoningEfforts: efforts,
		Source:                    SourceUpstream,
	}, true
}

func normalizeEfforts(raw codex.BackendModelEntry) []ReasoningEffort {
	out := make([]ReasoningEffort, 0)
	for _, effort := range raw.SupportedReasoningEfforts {
		name := firstNonEmpty(effort.ReasoningEffort, effort.ReasoningEffortAlt, effort.Effort)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, ReasoningEffort{
			ReasoningEffort: name,
			Description:     strings.TrimSpace(effort.Description),
		})
	}
	for _, effort := range raw.SupportedReasoningLevels {
		name := strings.TrimSpace(effort.Effort)
		if name == "" {
			continue
		}
		out = append(out, ReasoningEffort{
			ReasoningEffort: name,
			Description:     strings.TrimSpace(effort.Description),
		})
	}
	if len(out) == 0 {
		out = []ReasoningEffort{
			{ReasoningEffort: "medium", Description: "Default"},
		}
	}
	return dedupeEfforts(out)
}

func dedupeEfforts(efforts []ReasoningEffort) []ReasoningEffort {
	seen := make(map[string]struct{}, len(efforts))
	out := make([]ReasoningEffort, 0, len(efforts))
	for _, effort := range efforts {
		key := strings.TrimSpace(effort.ReasoningEffort)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, effort)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
