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
	if len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	model, ok := payload["model"].(string)
	if !ok {
		return body
	}
	normalized := normalizeModelName(model)
	if normalized == model {
		return body
	}
	payload["model"] = normalized
	next, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return next
}
