package wsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"chatgpt-codex-proxy/internal/codex"
)

type Client struct{}

func New() *Client {
	return &Client{}
}

type Stream struct {
	conn *websocket.Conn
}

func (c *Client) Connect(ctx context.Context, endpoint string, headers http.Header, body any) (*Stream, error) {
	dialer := websocket.Dialer{}
	conn, resp, err := dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed: %s", resp.Status)
		}
		return nil, err
	}
	if err := conn.WriteJSON(body); err != nil {
		conn.Close()
		return nil, err
	}
	return &Stream{conn: conn}, nil
}

func (s *Stream) Close() error {
	return s.conn.Close()
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
