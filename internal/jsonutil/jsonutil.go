// Package jsonutil provides helpers for decoded JSON trees shared across packages.
package jsonutil

import "strings"

// StringValue returns value as a string, or "" if it is not a string.
func StringValue(value any) string {
	str, _ := value.(string)
	return str
}

// FirstNonEmpty returns the first string that is not all whitespace.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// MapValue returns the nested map for key, or nil if the value is not a map.
func MapValue(raw map[string]any, key string) map[string]any {
	if raw == nil {
		return nil
	}
	value, _ := raw[key].(map[string]any)
	return value
}

// PathMapValue returns the nested map at the end of keys, or nil if any step is missing.
func PathMapValue(raw map[string]any, keys ...string) map[string]any {
	current := raw
	for idx, key := range keys {
		if current == nil {
			return nil
		}
		value, _ := current[key].(map[string]any)
		if idx == len(keys)-1 {
			return value
		}
		current = value
	}
	return nil
}

// CloneMap recursively clones nested map[string]any trees.
func CloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if mapped, ok := value.(map[string]any); ok {
			dst[key] = CloneMap(mapped)
			continue
		}
		dst[key] = value
	}
	return dst
}
