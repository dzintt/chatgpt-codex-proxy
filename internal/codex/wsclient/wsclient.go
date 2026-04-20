package wsclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/gorilla/websocket"

	"chatgpt-codex-proxy/internal/codex"
)

type Client struct{}

func New() *Client {
	return &Client{}
}

type Stream struct {
	conn    *websocket.Conn
	headers http.Header
}

func (c *Client) Connect(ctx context.Context, endpoint string, headers http.Header, body any) (*Stream, error) {
	dialer := websocket.Dialer{}
	conn, resp, err := dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		if resp != nil {
			payload, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, codex.NewUpstreamError("websocket dial", resp.StatusCode, string(payload), resp.Header)
		}
		return nil, err
	}
	if err := conn.WriteJSON(body); err != nil {
		conn.Close()
		return nil, err
	}
	return &Stream{
		conn:    conn,
		headers: cloneHeaders(resp),
	}, nil
}

func (s *Stream) Close() error {
	return s.conn.Close()
}

func (s *Stream) Headers() http.Header {
	return s.headers.Clone()
}

func (s *Stream) NextEvent() (*codex.StreamEvent, error) {
	_, message, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(message, &raw); err != nil {
		return nil, err
	}
	eventType := ""
	if typ, ok := raw["type"].(string); ok {
		eventType = typ
	}
	if strings.TrimSpace(eventType) == "" {
		return nil, io.EOF
	}
	return &codex.StreamEvent{Type: eventType, Raw: raw}, nil
}

func cloneHeaders(resp *http.Response) http.Header {
	if resp == nil {
		return make(http.Header)
	}
	out := make(http.Header, len(resp.Header))
	for key, values := range resp.Header {
		out[textproto.CanonicalMIMEHeaderKey(key)] = append([]string(nil), values...)
	}
	return out
}
