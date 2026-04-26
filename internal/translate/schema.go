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
	defs := schemaDefinitions(cloned)
	resolved := resolveLocalRefs(cloned, defs, nil)
	delete(resolved, "$defs")
	delete(resolved, "definitions")
	return injectAdditionalProperties(resolved)
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

	forEachSchemaChild(node, func(child map[string]any) {
		injectAdditionalProperties(child)
	})

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

func schemaDefinitions(schema map[string]any) map[string]map[string]any {
	defs := make(map[string]map[string]any)
	for _, key := range []string{"$defs", "definitions"} {
		rawDefs, ok := schema[key].(map[string]any)
		if !ok {
			continue
		}
		for name, raw := range rawDefs {
			definition, ok := raw.(map[string]any)
			if ok {
				defs["#/"+key+"/"+name] = definition
			}
		}
	}
	return defs
}

func resolveLocalRefs(node map[string]any, defs map[string]map[string]any, resolving map[string]bool) map[string]any {
	if node == nil {
		return nil
	}
	ref, _ := node["$ref"].(string)
	if ref != "" {
		if resolving[ref] {
			return node
		}
		definition, ok := defs[ref]
		if !ok {
			return node
		}
		nextResolving := cloneBoolMap(resolving)
		nextResolving[ref] = true
		resolved := cloneJSONMap(definition)
		if resolved == nil {
			return node
		}
		resolved = resolveLocalRefs(resolved, defs, nextResolving)
		for key, value := range node {
			if key != "$ref" {
				resolved[key] = value
			}
		}
		node = resolved
	}

	resolveLocalRefChildren(node, defs, resolving)

	return node
}

func resolveLocalRefChildren(node map[string]any, defs map[string]map[string]any, resolving map[string]bool) {
	if node == nil {
		return
	}

	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions"} {
		children, ok := node[key].(map[string]any)
		if !ok {
			continue
		}
		for name, raw := range children {
			if child, ok := raw.(map[string]any); ok {
				children[name] = resolveLocalRefs(child, defs, resolving)
			}
		}
	}

	if child, ok := node["items"].(map[string]any); ok {
		node["items"] = resolveLocalRefs(child, defs, resolving)
	}

	if prefixItems, ok := node["prefixItems"].([]any); ok {
		for index, raw := range prefixItems {
			if child, ok := raw.(map[string]any); ok {
				prefixItems[index] = resolveLocalRefs(child, defs, resolving)
			}
		}
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		entries, ok := node[key].([]any)
		if !ok {
			continue
		}
		for index, raw := range entries {
			if child, ok := raw.(map[string]any); ok {
				entries[index] = resolveLocalRefs(child, defs, resolving)
			}
		}
	}

	for _, key := range []string{"if", "then", "else", "not"} {
		if child, ok := node[key].(map[string]any); ok {
			node[key] = resolveLocalRefs(child, defs, resolving)
		}
	}
}

func cloneBoolMap(value map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(value)+1)
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func forEachSchemaChild(node map[string]any, visit func(map[string]any)) {
	if node == nil || visit == nil {
		return
	}

	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions"} {
		children, ok := node[key].(map[string]any)
		if !ok {
			continue
		}
		for _, raw := range children {
			child, ok := raw.(map[string]any)
			if ok {
				visit(child)
			}
		}
	}

	if items, ok := node["items"].(map[string]any); ok {
		visit(items)
	}

	if prefixItems, ok := node["prefixItems"].([]any); ok {
		for _, raw := range prefixItems {
			child, ok := raw.(map[string]any)
			if ok {
				visit(child)
			}
		}
	}

	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		entries, ok := node[key].([]any)
		if !ok {
			continue
		}
		for _, raw := range entries {
			child, ok := raw.(map[string]any)
			if ok {
				visit(child)
			}
		}
	}

	for _, key := range []string{"if", "then", "else", "not"} {
		child, ok := node[key].(map[string]any)
		if ok {
			visit(child)
		}
	}
}

func anySchemaChild(node map[string]any, match func(map[string]any) bool) bool {
	found := false
	forEachSchemaChild(node, func(child map[string]any) {
		if found {
			return
		}
		found = match(child)
	})
	return found
}
