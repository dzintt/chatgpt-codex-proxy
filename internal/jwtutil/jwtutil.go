// Package jwtutil provides small helpers for decoding JWT payloads.
package jwtutil

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// DecodePayload decodes the middle JWT segment into target.
func DecodePayload(token string, target any) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	return json.Unmarshal(payload, target) == nil
}
