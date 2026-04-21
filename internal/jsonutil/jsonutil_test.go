package jsonutil

import "testing"

func TestFirstNonEmptySkipsWhitespaceOnlyStrings(t *testing.T) {
	t.Parallel()

	got := FirstNonEmpty("", "   ", " value ", "fallback")
	if got != " value " {
		t.Fatalf("FirstNonEmpty() = %q, want original first non-empty string", got)
	}
}

func TestMapValueAndPathMapValue(t *testing.T) {
	t.Parallel()

	tree := map[string]any{
		"response": map[string]any{
			"error": map[string]any{
				"message": "boom",
			},
		},
	}

	if got := MapValue(tree, "response"); got == nil {
		t.Fatalf("MapValue() = %#v, want nested map", got)
	}
	if got := PathMapValue(tree, "response", "error"); got == nil || StringValue(got["message"]) != "boom" {
		t.Fatalf("PathMapValue() = %#v, want nested map", got)
	}
}

func TestCloneMapDeepClonesNestedMaps(t *testing.T) {
	t.Parallel()

	src := map[string]any{
		"outer": map[string]any{
			"inner": "value",
		},
	}

	cloned := CloneMap(src)
	clonedOuter := cloned["outer"].(map[string]any)
	clonedOuter["inner"] = "changed"

	if src["outer"].(map[string]any)["inner"] != "value" {
		t.Fatal("CloneMap() did not isolate nested map mutations")
	}
}
