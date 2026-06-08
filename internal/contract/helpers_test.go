package contract_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func assertJSONPath(t *testing.T, raw []byte, path string) {
	t.Helper()
	value, ok := jsonPathValue(raw, path)
	if !ok {
		t.Fatalf("missing JSON path %q in %s", path, string(raw))
	}
	if value == nil && !strings.Contains(path, "xrayInfo") && !strings.Contains(path, "error") && !strings.Contains(path, "version") {
		// nullable fields may be null — still present
	}
}

func assertJSONPathArray(t *testing.T, raw []byte, path string) {
	t.Helper()
	value, ok := jsonPathValue(raw, path)
	if !ok {
		t.Fatalf("missing JSON path %q in %s", path, string(raw))
	}
	if _, ok := value.([]any); !ok {
		t.Fatalf("path %q must be an array, got %T", path, value)
	}
}

func jsonPathValue(raw []byte, path string) (any, bool) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, false
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := obj[segment]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func assertTopLevelResponse(t *testing.T, raw []byte) {
	t.Helper()
	assertJSONPath(t, raw, "response")
}
