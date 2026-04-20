package openai

import "strings"

const CanonicalDefaultModel = "gpt-5.3-codex"
const ModelCreatedTimestamp int64 = 1700000000

var supportedModelSet = map[string]struct{}{
	"gpt-5.4":          {},
	"gpt-5.4-mini":     {},
	"gpt-5.3-codex":    {},
	"gpt-5.2":          {},
	"gpt-5-codex":      {},
	"gpt-5-codex-mini": {},
	"gpt-oss-120b":     {},
	"gpt-oss-20b":      {},
}

func ResolveDefaultModel(configured string) string {
	model := strings.TrimSpace(configured)
	if model == "" {
		return CanonicalDefaultModel
	}
	if _, ok := supportedModelSet[model]; ok {
		return model
	}
	return CanonicalDefaultModel
}

func PublicModelList(configuredDefault string) []string {
	defaultModel := ResolveDefaultModel(configuredDefault)
	ordered := []string{
		"codex",
		defaultModel,
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex",
		"gpt-5.2",
		"gpt-5-codex",
		"gpt-5-codex-mini",
		"gpt-oss-120b",
		"gpt-oss-20b",
	}
	seen := make(map[string]struct{}, len(ordered))
	result := make([]string, 0, len(ordered))
	for _, model := range ordered {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	return result
}

func ResolvePublicModel(model string, configuredDefault string) (string, bool) {
	target := strings.TrimSpace(model)
	if target == "" {
		return "", false
	}
	for _, publicModel := range PublicModelList(configuredDefault) {
		if publicModel == target {
			return publicModel, true
		}
	}
	return "", false
}
