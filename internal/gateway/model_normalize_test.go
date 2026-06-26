package gateway

import (
	"encoding/json"
	"testing"
)

func TestNormalizeModelNameRemovesDurationSuffix(t *testing.T) {
	cases := map[string]string{
		"umans-glm-5.2[1m]":        "umans-glm-5.2",
		"claude-opus-4-8[1m]":      "claude-opus-4-8",
		"claude-sonnet-4-6[30s]":   "claude-sonnet-4-6",
		"umans-glm-5.2":            "umans-glm-5.2",
		"umans-glm-5.2-preview[x]": "umans-glm-5.2-preview[x]",
	}
	for in, want := range cases {
		if got := normalizeModelName(in); got != want {
			t.Fatalf("normalizeModelName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormalizeModelInJSONOnlyChangesTopLevelModel(t *testing.T) {
	body := normalizeModelInJSON([]byte(`{"model":"umans-glm-5.2[1m]","messages":[{"role":"user","content":"keep umans-glm-5.2[1m] here"}]}`))
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["model"]; got != "umans-glm-5.2" {
		t.Fatalf("model=%v", got)
	}
	messages := payload["messages"].([]any)
	msg := messages[0].(map[string]any)
	if got := msg["content"]; got != "keep umans-glm-5.2[1m] here" {
		t.Fatalf("content changed: %v", got)
	}
}
