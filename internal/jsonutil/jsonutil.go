// Package jsonutil contains helpers for working with decoded JSON
// (map[string]any / []any trees) shared between packages.
package jsonutil

// StringValue returns value as a string, or "" if it is not a string.
func StringValue(value any) string {
	str, _ := value.(string)
	return str
}
