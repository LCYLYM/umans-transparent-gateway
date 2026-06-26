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

func TestNormalizeRequestJSONConvertsTupleItemsSchema(t *testing.T) {
	body := normalizeRequestJSON([]byte(`{
	  "model":"umans-glm-5.2[1m]",
	  "tools":[{
	    "name":"plot",
	    "input_schema":{
	      "type":"object",
	      "properties":{
	        "coordinate":{
	          "type":"array",
	          "items":[{"type":"number"},{"type":"number"}],
	          "additionalItems":false
	        }
	      }
	    }
	  }]
	}`))
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["model"]; got != "umans-glm-5.2" {
		t.Fatalf("model=%v", got)
	}
	tools := payload["tools"].([]any)
	tool := tools[0].(map[string]any)
	schema := tool["input_schema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	coordinate := properties["coordinate"].(map[string]any)
	if _, ok := coordinate["items"].([]any); ok {
		t.Fatalf("items still uses tuple array: %#v", coordinate["items"])
	}
	if got := coordinate["items"]; got != false {
		t.Fatalf("items=%#v, want false", got)
	}
	prefixItems, ok := coordinate["prefixItems"].([]any)
	if !ok || len(prefixItems) != 2 {
		t.Fatalf("prefixItems=%#v", coordinate["prefixItems"])
	}
	if _, exists := coordinate["additionalItems"]; exists {
		t.Fatalf("additionalItems should be removed")
	}
}

func TestNormalizeRequestJSONAllowsAdditionalTupleItemsByDefault(t *testing.T) {
	body := normalizeRequestJSON([]byte(`{"tools":[{"function":{"parameters":{"type":"object","properties":{"pair":{"type":"array","items":[{"type":"number"},{"type":"number"}]}}}}}]}`))
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	tools := payload["tools"].([]any)
	tool := tools[0].(map[string]any)
	fn := tool["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	pair := props["pair"].(map[string]any)
	if got := pair["items"]; got != true {
		t.Fatalf("items=%#v, want true", got)
	}
	if prefixItems := pair["prefixItems"].([]any); len(prefixItems) != 2 {
		t.Fatalf("prefixItems=%#v", prefixItems)
	}
}
