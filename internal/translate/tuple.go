package translate

import (
	"strconv"
	"strings"
)

func HasTupleSchemas(schema map[string]any) bool {
	return hasTupleSchemas(schema)
}

func hasTupleSchemas(node map[string]any) bool {
	if node == nil {
		return false
	}
	if _, ok := node["prefixItems"].([]any); ok {
		return true
	}

	if properties, ok := node["properties"].(map[string]any); ok {
		for _, raw := range properties {
			if child, ok := raw.(map[string]any); ok && hasTupleSchemas(child) {
				return true
			}
		}
	}

	if items, ok := node["items"].(map[string]any); ok && hasTupleSchemas(items) {
		return true
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if entries, ok := node[key].([]any); ok {
			for _, raw := range entries {
				if child, ok := raw.(map[string]any); ok && hasTupleSchemas(child) {
					return true
				}
			}
		}
	}

	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := node[key].(map[string]any); ok {
			for _, raw := range defs {
				if child, ok := raw.(map[string]any); ok && hasTupleSchemas(child) {
					return true
				}
			}
		}
	}

	for _, key := range []string{"if", "then", "else", "not"} {
		if child, ok := node[key].(map[string]any); ok && hasTupleSchemas(child) {
			return true
		}
	}

	return false
}

func ConvertTupleSchemas(node map[string]any) map[string]any {
	return convertTupleSchemas(node)
}

func convertTupleSchemas(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}

	if prefixItems, ok := node["prefixItems"].([]any); ok {
		properties := make(map[string]any, len(prefixItems))
		required := make([]string, 0, len(prefixItems))
		for i, raw := range prefixItems {
			key := intString(i)
			if child, ok := raw.(map[string]any); ok {
				properties[key] = convertTupleSchemas(child)
			} else {
				properties[key] = raw
			}
			required = append(required, key)
		}
		node["type"] = "object"
		node["properties"] = properties
		node["required"] = required
		node["additionalProperties"] = false
		delete(node, "prefixItems")
		delete(node, "items")
		return node
	}

	if properties, ok := node["properties"].(map[string]any); ok {
		for key, raw := range properties {
			if child, ok := raw.(map[string]any); ok {
				properties[key] = convertTupleSchemas(child)
			}
		}
	}

	if items, ok := node["items"].(map[string]any); ok {
		node["items"] = convertTupleSchemas(items)
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if entries, ok := node[key].([]any); ok {
			for i, raw := range entries {
				if child, ok := raw.(map[string]any); ok {
					entries[i] = convertTupleSchemas(child)
				}
			}
		}
	}

	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := node[key].(map[string]any); ok {
			for childKey, raw := range defs {
				if child, ok := raw.(map[string]any); ok {
					defs[childKey] = convertTupleSchemas(child)
				}
			}
		}
	}

	for _, key := range []string{"if", "then", "else", "not"} {
		if child, ok := node[key].(map[string]any); ok {
			node[key] = convertTupleSchemas(child)
		}
	}

	return node
}

func ReconvertTupleValues(data any, schema map[string]any) any {
	return reconvertTupleValues(data, schema, schema)
}

func reconvertTupleValues(data any, schema map[string]any, root map[string]any) any {
	if schema == nil {
		return data
	}
	if ref, _ := schema["$ref"].(string); strings.TrimSpace(ref) != "" {
		if resolved := resolveTupleSchemaRef(ref, root); resolved != nil {
			return reconvertTupleValues(data, resolved, root)
		}
		return data
	}

	if prefixItems, ok := schema["prefixItems"].([]any); ok {
		mapped, ok := data.(map[string]any)
		if !ok {
			return data
		}
		out := make([]any, 0, len(prefixItems))
		for i, raw := range prefixItems {
			key := intString(i)
			value := mapped[key]
			if child, ok := raw.(map[string]any); ok {
				out = append(out, reconvertTupleValues(value, child, root))
				continue
			}
			out = append(out, value)
		}
		return out
	}

	if properties, ok := schema["properties"].(map[string]any); ok {
		mapped, ok := data.(map[string]any)
		if !ok {
			return data
		}
		out := make(map[string]any, len(mapped))
		for key, value := range mapped {
			out[key] = value
		}
		for key, raw := range properties {
			if child, ok := raw.(map[string]any); ok {
				if value, ok := out[key]; ok {
					out[key] = reconvertTupleValues(value, child, root)
				}
			}
		}
		return out
	}

	if items, ok := schema["items"].(map[string]any); ok {
		values, ok := data.([]any)
		if !ok {
			return data
		}
		out := make([]any, len(values))
		for i, value := range values {
			out[i] = reconvertTupleValues(value, items, root)
		}
		return out
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if entries, ok := schema[key].([]any); ok {
			for _, raw := range entries {
				if child, ok := raw.(map[string]any); ok && hasTupleSchemas(child) {
					return reconvertTupleValues(data, child, root)
				}
			}
		}
	}

	return data
}

func resolveTupleSchemaRef(ref string, root map[string]any) map[string]any {
	if root == nil {
		return nil
	}
	parts := strings.Split(ref, "/")
	if len(parts) != 3 || parts[0] != "#" {
		return nil
	}
	if parts[1] != "$defs" && parts[1] != "definitions" {
		return nil
	}
	defs, ok := root[parts[1]].(map[string]any)
	if !ok {
		return nil
	}
	resolved, _ := defs[parts[2]].(map[string]any)
	return resolved
}

func intString(value int) string {
	return strconv.Itoa(value)
}
