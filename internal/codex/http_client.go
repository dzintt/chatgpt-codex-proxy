package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strings"
	"sync"

	httpcloak "github.com/sardanioss/httpcloak/client"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/config"
)

type HTTPClient struct {
	cfg      config.Config
	mu       sync.Mutex
	sessions map[string]*httpcloak.Client
}

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

	resp, err := session.Get(ctx, strings.TrimRight(c.cfg.CodexBaseURL, "/")+"/codex/usage", headers)
	if err != nil {
		return UsageResponse{}, nil, err
	}
	defer resp.Close()

	payload, err := resp.Text()
	if err != nil {
		return UsageResponse{}, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UsageResponse{}, nil, fmt.Errorf("codex usage failed: %s", payload)
	}

	var decoded UsageResponse
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return UsageResponse{}, nil, err
	}
	return decoded, QuotaFromUsageResponse(decoded), nil
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

	bodyReq := req
	bodyReq.Stream = true
	bodyReq.Store = false
	bodyReq.PreviousResponseID = ""
	bodyReq.ServiceTier = ""
	payload, err := json.Marshal(bodyReq)
	if err != nil {
		return nil, err
	}

	streamResp, err := session.DoStream(ctx, &httpcloak.Request{
		Method:  http.MethodPost,
		URL:     strings.TrimRight(c.cfg.CodexBaseURL, "/") + "/codex/responses",
		Headers: headers,
		Body:    bytes.NewReader(payload),
	})
	if err != nil {
		return nil, err
	}
	if streamResp.StatusCode < 200 || streamResp.StatusCode >= 300 {
		data, _ := io.ReadAll(streamResp)
		streamResp.Close()
		return nil, fmt.Errorf("codex response failed (%d): %s", streamResp.StatusCode, strings.TrimSpace(string(data)))
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
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	eventType := strings.TrimSpace(eventName)
	if eventType == "" {
		eventType = stringValue(raw["type"])
	}
	return &StreamEvent{
		Type: eventType,
		Raw:  raw,
	}, nil
}

func toHTTPHeader(headers map[string][]string) http.Header {
	out := make(http.Header, len(headers))
	for key, values := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		out[canonical] = append([]string(nil), values...)
	}
	return out
}

func NewRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(buf)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[0:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}
