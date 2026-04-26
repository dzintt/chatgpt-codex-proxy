package translate

import "testing"

func TestPrepareSchemaWithoutTuplesInjectsAdditionalProperties(t *testing.T) {
	t.Parallel()

	prepared, tupleSchema := PrepareSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"child": map[string]any{"type": "object"},
		},
	})

	if tupleSchema != nil {
		t.Fatalf("tupleSchema = %#v, want nil", tupleSchema)
	}
	child, _ := prepared["properties"].(map[string]any)["child"].(map[string]any)
	if child["additionalProperties"] != false {
		t.Fatalf("child additionalProperties = %#v, want false", child["additionalProperties"])
	}
}

func TestNormalizeSchemaInlinesRefsAndInjectsAdditionalProperties(t *testing.T) {
	t.Parallel()

	normalized := NormalizeSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"candidates": map[string]any{
				"type": "array",
				"items": map[string]any{
					"$ref": "#/$defs/LeadCandidateIdentity",
				},
			},
		},
		"$defs": map[string]any{
			"LeadCandidateIdentity": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"business_name": map[string]any{"type": "string"},
					"website": map[string]any{
						"anyOf": []any{
							map[string]any{"type": "string"},
							map[string]any{"type": "null"},
						},
					},
				},
			},
		},
	})

	if _, ok := normalized["$defs"]; ok {
		t.Fatalf("$defs still present in normalized schema: %#v", normalized["$defs"])
	}
	rootProps, _ := normalized["properties"].(map[string]any)
	candidates, _ := rootProps["candidates"].(map[string]any)
	items, _ := candidates["items"].(map[string]any)
	if items["$ref"] != nil {
		t.Fatalf("items.$ref = %#v, want inlined schema", items["$ref"])
	}
	if items["additionalProperties"] != false {
		t.Fatalf("items.additionalProperties = %#v, want false", items["additionalProperties"])
	}
	itemProps, _ := items["properties"].(map[string]any)
	website, _ := itemProps["website"].(map[string]any)
	anyOf, _ := website["anyOf"].([]any)
	if len(anyOf) != 2 {
		t.Fatalf("website.anyOf len = %d, want 2", len(anyOf))
	}
}

func TestHasTupleSchemasDetectsNestedLocations(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"pair": map[string]any{
				"type": "array",
				"prefixItems": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "number"},
				},
			},
		},
	}

	if !HasTupleSchemas(schema) {
		t.Fatal("expected tuple schema detection")
	}
}

func TestConvertTupleSchemasConvertsPrefixItemsToObjectShape(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "array",
		"prefixItems": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "object"},
		},
	}

	converted := ConvertTupleSchemas(schema)
	if converted["type"] != "object" {
		t.Fatalf("type = %#v, want object", converted["type"])
	}
	properties, _ := converted["properties"].(map[string]any)
	if _, ok := properties["0"]; !ok {
		t.Fatalf("properties = %#v, want key 0", properties)
	}
	if _, ok := properties["1"]; !ok {
		t.Fatalf("properties = %#v, want key 1", properties)
	}
	if converted["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", converted["additionalProperties"])
	}
}

func TestReconvertTupleValuesRestoresArrays(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pair": map[string]any{
				"type": "array",
				"prefixItems": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "number"},
				},
			},
		},
	}

	reconverted := ReconvertTupleValues(map[string]any{
		"pair": map[string]any{
			"0": "left",
			"1": float64(2),
		},
	}, schema)

	root, _ := reconverted.(map[string]any)
	pair, ok := root["pair"].([]any)
	if !ok {
		t.Fatalf("pair = %#v, want []any", root["pair"])
	}
	if len(pair) != 2 || pair[0] != "left" || pair[1] != float64(2) {
		t.Fatalf("pair = %#v", pair)
	}
}
