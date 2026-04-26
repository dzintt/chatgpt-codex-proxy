package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	httpcloak "github.com/sardanioss/httpcloak/client"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/jsonutil"
)

type HTTPClient struct {
	cfg      config.Config
	mu       sync.Mutex
	sessions map[string]*httpcloak.Client
}

var requestSequence uint64

func NewHTTPClient(cfg config.Config) *HTTPClient {
	return &HTTPClient{
		cfg:      cfg,
		sessions: make(map[string]*httpcloak.Client),
	}
}

func (c *HTTPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, session := range c.sessions {
		session.Close()
	}
	c.sessions = make(map[string]*httpcloak.Client)
	return nil
}

func (c *HTTPClient) GetUsage(ctx context.Context, record accounts.Record) (UsageResponse, *accounts.QuotaSnapshot, error) {
	session := c.sessionFor(record.ID)
	headers := OrderedHeaders(BuildHeaders(c.cfg, record.Token.AccessToken, HeaderOptions{
		AccountID:      record.AccountID,
		Cookies:        record.Cookies,
		RequestID:      NewRequestID(),
		Accept:         "application/json",
		AcceptEncoding: "gzip, deflate",
	}), c.cfg.HeaderOrder)

	resp, err := session.Get(ctx, JoinURL(c.cfg.CodexBaseURL, "/codex/usage"), headers)
	if err != nil {
		return UsageResponse{}, nil, err
	}
	defer resp.Close()

	payload, err := resp.Text()
	if err != nil {
		return UsageResponse{}, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UsageResponse{}, nil, NewUpstreamError("codex usage", resp.StatusCode, payload, toHTTPHeader(resp.Headers))
	}

	var decoded UsageResponse
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return UsageResponse{}, nil, err
	}
	return decoded, QuotaFromUsageResponse(decoded), nil
}

func (c *HTTPClient) GetCodexModels(ctx context.Context, record accounts.Record) ([]BackendModelEntry, error) {
	session := c.sessionFor(record.ID)
	headers := OrderedHeaders(BuildHeaders(c.cfg, record.Token.AccessToken, HeaderOptions{
		AccountID:      record.AccountID,
		Cookies:        record.Cookies,
		RequestID:      NewRequestID(),
		Accept:         "application/json",
		AcceptEncoding: "gzip, deflate",
	}), c.cfg.HeaderOrder)

	endpoint := c.codexModelsURL()
	resp, err := session.Get(ctx, endpoint, headers)
	if err != nil {
		return nil, err
	}
	payload, err := resp.Text()
	resp.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, NewUpstreamError("codex models", resp.StatusCode, payload, toHTTPHeader(resp.Headers))
	}

	models, err := parseCodexModelsResponse(payload)
	if err != nil {
		return nil, fmt.Errorf("codex models returned an unusable body: %w", err)
	}
	return models, nil
}

func (c *HTTPClient) CompactResponse(ctx context.Context, record accounts.Record, req CompactRequest) (CompactResponse, *accounts.QuotaSnapshot, error) {
	session := c.sessionFor(record.ID)
	headers := OrderedHeaders(BuildHeaders(c.cfg, record.Token.AccessToken, HeaderOptions{
		AccountID:      record.AccountID,
		Cookies:        record.Cookies,
		ContentType:    "application/json",
		RequestID:      NewRequestID(),
		IncludeBeta:    true,
		Accept:         "application/json",
		AcceptEncoding: "gzip, deflate",
	}), c.cfg.HeaderOrder)

	payload, err := json.Marshal(req)
	if err != nil {
		return CompactResponse{}, nil, err
	}

	resp, err := session.Do(ctx, &httpcloak.Request{
		Method:  http.MethodPost,
		URL:     JoinURL(c.cfg.CodexBaseURL, "/codex/responses/compact"),
		Headers: headers,
		Body:    bytes.NewReader(payload),
	})
	if err != nil {
		return CompactResponse{}, nil, err
	}
	defer resp.Close()

	body, err := resp.Text()
	if err != nil {
		return CompactResponse{}, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CompactResponse{}, nil, NewUpstreamError("codex compact response", resp.StatusCode, body, toHTTPHeader(resp.Headers))
	}

	decoded, err := parseCompactResponse(body)
	if err != nil {
		return CompactResponse{}, nil, err
	}
	return decoded, ParseQuotaFromHeaders(toHTTPHeader(resp.Headers)), nil
}

func (c *HTTPClient) StreamResponse(ctx context.Context, record accounts.Record, req Request, turnState string) (*StreamReader, error) {
	session := c.sessionFor(record.ID)
	headers := OrderedHeaders(BuildHeaders(c.cfg, record.Token.AccessToken, HeaderOptions{
		AccountID:   record.AccountID,
		Cookies:     record.Cookies,
		ContentType: "application/json",
		TurnState:   turnState,
		RequestID:   NewRequestID(),
		IncludeBeta: true,
		Accept:      "text/event-stream",
	}), c.cfg.HeaderOrder)

	bodyReq := StreamRequestPayload(req)
	payload, err := json.Marshal(bodyReq)
	if err != nil {
		return nil, err
	}

	streamResp, err := session.DoStream(ctx, &httpcloak.Request{
		Method:  http.MethodPost,
		URL:     JoinURL(c.cfg.CodexBaseURL, "/codex/responses"),
		Headers: headers,
		Body:    bytes.NewReader(payload),
	})
	if err != nil {
		return nil, err
	}
	if streamResp.StatusCode < 200 || streamResp.StatusCode >= 300 {
		data := readLimitedErrorBody(streamResp)
		headers := toHTTPHeader(streamResp.Headers)
		streamResp.Close()
		return nil, NewUpstreamError("codex response", streamResp.StatusCode, data, headers)
	}

	return &StreamReader{
		reader:  bufio.NewReader(streamResp),
		closer:  streamResp,
		headers: toHTTPHeader(streamResp.Headers),
	}, nil
}

func (c *HTTPClient) sessionFor(accountID string) *httpcloak.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.sessions[accountID]; ok {
		return existing
	}
	session := httpcloak.NewSession(
		"chrome-latest",
		httpcloak.WithTimeout(c.cfg.RequestTimeout),
		httpcloak.WithoutRetry(),
	)
	c.sessions[accountID] = session
	return session
}

type StreamReader struct {
	reader  *bufio.Reader
	closer  io.Closer
	headers http.Header
}

func (r *StreamReader) Headers() http.Header {
	return r.headers.Clone()
}

func (r *StreamReader) Close() error {
	return r.closer.Close()
}

func (r *StreamReader) NextEvent() (*StreamEvent, error) {
	var eventName string
	var dataLines []string
	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(dataLines) > 0 {
				return parseStreamEvent(eventName, strings.Join(dataLines, "\n"))
			}
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(dataLines) == 0 {
				continue
			}
			return parseStreamEvent(eventName, strings.Join(dataLines, "\n"))
		}
		if strings.HasPrefix(strings.ToLower(line), "event:") {
			eventName = strings.TrimSpace(line[6:])
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "data:") {
			dataLines = append(dataLines, strings.TrimSpace(line[5:]))
		}
	}
}

func parseStreamEvent(eventName, data string) (*StreamEvent, error) {
	if data == "" || data == "[DONE]" {
		return nil, io.EOF
	}
	var raw map[string]any
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	eventType := strings.TrimSpace(eventName)
	if eventType == "" {
		eventType = jsonutil.StringValue(raw["type"])
	}
	return &StreamEvent{
		Type: eventType,
		Raw:  raw,
	}, nil
}

// CanonicalHeader copies headers into an http.Header, canonicalizing keys.
func CanonicalHeader(headers map[string][]string) http.Header {
	out := make(http.Header, len(headers))
	for key, values := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		out[canonical] = append([]string(nil), values...)
	}
	return out
}

func toHTTPHeader(headers map[string][]string) http.Header { return CanonicalHeader(headers) }

// JoinURL trims trailing slashes from base and appends path.
func JoinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func NewRequestID() string {
	return fmt.Sprintf("req_%d_%08x", time.Now().UTC().UnixNano(), atomic.AddUint64(&requestSequence, 1))
}

func StreamRequestPayload(req Request) Request {
	bodyReq := req
	bodyReq.Stream = true
	bodyReq.Store = false
	bodyReq.PreviousResponseID = ""
	return bodyReq
}

func parseCompactResponse(payload string) (CompactResponse, error) {
	var decoded CompactResponse
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return CompactResponse{}, err
	}
	return decoded, nil
}

func (c *HTTPClient) codexModelsURL() string {
	base := JoinURL(c.cfg.CodexBaseURL, "/codex/models")
	if strings.TrimSpace(c.cfg.ClientVersion) == "" {
		return base
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := parsed.Query()
	query.Set("client_version", c.cfg.ClientVersion)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func parseCodexModelsResponse(payload string) ([]BackendModelEntry, error) {
	var decoded struct {
		Models json.RawMessage `json:"models"`
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Models) == 0 {
		return nil, fmt.Errorf("missing models field")
	}
	var models []BackendModelEntry
	if err := json.Unmarshal(decoded.Models, &models); err != nil {
		return nil, fmt.Errorf("models field is not a model array: %w", err)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("models array is empty")
	}
	for idx, model := range models {
		if strings.TrimSpace(model.Slug) == "" && strings.TrimSpace(model.ID) == "" && strings.TrimSpace(model.Name) == "" {
			return nil, fmt.Errorf("models[%d] is missing slug, id, and name", idx)
		}
	}
	return models, nil
}
