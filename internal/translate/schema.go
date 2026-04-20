package translate

import "encoding/json"

func PrepareSchema(schema map[string]any) (prepared map[string]any, tupleSchema map[string]any) {
	cloned := cloneJSONMap(schema)
	if cloned == nil {
		return nil, nil
	}
	if !HasTupleSchemas(cloned) {
		return injectAdditionalProperties(cloned), nil
	}

	original := cloneJSONMap(schema)
	ConvertTupleSchemas(cloned)
	return injectAdditionalProperties(cloned), original
}

func NormalizeSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return schema
	}
	cloned := cloneJSONMap(schema)
	if cloned == nil {
		return schema
	}
	return normalizeObjectProperties(cloned)
}

func injectAdditionalProperties(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}

	node = normalizeObjectProperties(node)
	if node["type"] == "object" {
		if _, ok := node["additionalProperties"]; !ok {
			node["additionalProperties"] = false
		}
	}

	if properties, ok := node["properties"].(map[string]any); ok {
		for key, raw := range properties {
			if child, ok := raw.(map[string]any); ok {
				properties[key] = injectAdditionalProperties(child)
			}
		}
	}

	if patternProperties, ok := node["patternProperties"].(map[string]any); ok {
		for key, raw := range patternProperties {
			if child, ok := raw.(map[string]any); ok {
				patternProperties[key] = injectAdditionalProperties(child)
			}
		}
	}

	for _, defsKey := range []string{"$defs", "definitions"} {
		if defs, ok := node[defsKey].(map[string]any); ok {
			for key, raw := range defs {
				if child, ok := raw.(map[string]any); ok {
					defs[key] = injectAdditionalProperties(child)
				}
			}
		}
	}

	if items, ok := node["items"].(map[string]any); ok {
		node["items"] = injectAdditionalProperties(items)
	}

	if prefixItems, ok := node["prefixItems"].([]any); ok {
		for i, raw := range prefixItems {
			if child, ok := raw.(map[string]any); ok {
				prefixItems[i] = injectAdditionalProperties(child)
			}
		}
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if entries, ok := node[key].([]any); ok {
			for i, raw := range entries {
				if child, ok := raw.(map[string]any); ok {
					entries[i] = injectAdditionalProperties(child)
				}
			}
		}
	}

	for _, key := range []string{"if", "then", "else", "not"} {
		if child, ok := node[key].(map[string]any); ok {
			node[key] = injectAdditionalProperties(child)
		}
	}

	return node
}

func normalizeObjectProperties(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}
	if node["type"] == "object" {
		if _, ok := node["properties"]; !ok {
			node["properties"] = map[string]any{}
		}
	}
	return node
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var cloned map[string]any
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil
	}
	return cloned
}
