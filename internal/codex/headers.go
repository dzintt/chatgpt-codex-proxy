package codex

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"chatgpt-codex-proxy/internal/config"
)

type HeaderOptions struct {
	AccountID   string
	Cookies     map[string]string
	ContentType string
	TurnState   string
	RequestID   string
}

func BuildHeaders(cfg config.Config, token string, opts HeaderOptions) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	if opts.AccountID != "" {
		headers.Set("ChatGPT-Account-Id", opts.AccountID)
	}
	headers.Set("originator", cfg.Originator)
	headers.Set("x-openai-internal-codex-residency", cfg.Residency)
	headers.Set("x-client-request-id", opts.RequestID)
	if opts.TurnState != "" {
		headers.Set("x-codex-turn-state", opts.TurnState)
	}
	headers.Set("OpenAI-Beta", cfg.OpenAIBeta)
	headers.Set("User-Agent", strings.NewReplacer(
		"{platform}", cfg.Platform,
		"{arch}", cfg.Arch,
	).Replace(cfg.UserAgentTemplate))
	headers.Set("sec-ch-ua", fmt.Sprintf(`"Chromium";v="%s", "Not:A-Brand";v="24"`, cfg.ChromiumVersion))
	headers.Set("sec-ch-ua-mobile", "?0")
	headers.Set("sec-ch-ua-platform", fmt.Sprintf(`"%s"`, cfg.Platform))
	headers.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	headers.Set("Accept-Language", cfg.DefaultAcceptLanguage)
	headers.Set("sec-fetch-site", "same-origin")
	headers.Set("sec-fetch-mode", "cors")
	headers.Set("sec-fetch-dest", "empty")
	if opts.ContentType != "" {
		headers.Set("Content-Type", opts.ContentType)
	}
	if len(opts.Cookies) > 0 {
		headers.Set("Cookie", cookieHeader(opts.Cookies))
	}
	return headers
}

func OrderedHeaders(headers http.Header, order []string) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		out[key] = append([]string(nil), values...)
	}
	if len(order) == 0 {
		return out
	}

	ordered := make(map[string][]string, len(headers))
	seen := make(map[string]struct{}, len(headers))
	lowerKeys := make(map[string]string, len(headers))
	for key := range headers {
		lowerKeys[strings.ToLower(key)] = key
	}
	for _, key := range order {
		if original, ok := lowerKeys[key]; ok {
			ordered[original] = append([]string(nil), headers[original]...)
			seen[original] = struct{}{}
		}
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		ordered[key] = append([]string(nil), headers[key]...)
	}
	return ordered
}

func cookieHeader(cookies map[string]string) string {
	pairs := make([]string, 0, len(cookies))
	keys := make([]string, 0, len(cookies))
	for key := range cookies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", key, url.QueryEscape(cookies[key])))
	}
	return strings.Join(pairs, "; ")
}
