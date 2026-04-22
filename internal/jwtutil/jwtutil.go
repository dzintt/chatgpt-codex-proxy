// Package jwtutil provides small helpers for decoding JWT payloads.
package jwtutil

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// DecodePayload decodes the middle JWT segment into a typed value.
func DecodePayload[T any](token string) (T, bool) {
	var zero T
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return zero, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, false
	}
	var decoded T
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return zero, false
	}
	return decoded, true
}
