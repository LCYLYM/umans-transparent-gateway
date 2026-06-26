package gateway

import (
	"encoding/json"
	"regexp"
	"strings"
)

var modelDurationSuffix = regexp.MustCompile(`\[(?:\d+[smhd]|1m)\]$`)

func normalizeModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	return modelDurationSuffix.ReplaceAllString(model, "")
}

func normalizeModelInJSON(body []byte) []byte {
	return normalizeRequestJSON(body)
}

func normalizeRequestJSON(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	changed := normalizeJSONSchemaDraft(&payload)
	model, ok := payload["model"].(string)
	if ok {
		normalized := normalizeModelName(model)
		if normalized != model {
			payload["model"] = normalized
			changed = true
		}
	}
	if !changed {
		return body
	}
	next, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return next
}

func normalizeJSONSchemaDraft(v any) bool {
	changed := false
	switch x := v.(type) {
	case *map[string]any:
		if x == nil {
			return false
		}
		changed = normalizeJSONSchemaDraftMap(*x)
	case map[string]any:
		changed = normalizeJSONSchemaDraftMap(x)
	case []any:
		for _, item := range x {
			if normalizeJSONSchemaDraft(item) {
				changed = true
			}
		}
	}
	return changed
}

func normalizeJSONSchemaDraftMap(m map[string]any) bool {
	changed := false
	for _, v := range m {
		if normalizeJSONSchemaDraft(v) {
			changed = true
		}
	}
	items, ok := m["items"].([]any)
	if !ok {
		return changed
	}
	if _, exists := m["prefixItems"]; !exists {
		m["prefixItems"] = items
	}
	if additional, exists := m["additionalItems"]; exists {
		switch additional.(type) {
		case bool, map[string]any:
			m["items"] = additional
		default:
			m["items"] = true
		}
		delete(m, "additionalItems")
	} else {
		m["items"] = true
	}
	return true
}
