package gateway

import "testing"

func TestParseCatalogAndSafeTier(t *testing.T) {
	raw := map[string]any{
		"umans-coder":   map[string]any{"display_name": "Coder", "capabilities": map[string]any{"context_window": float64(400000), "max_completion_tokens": float64(65536), "recommended_max_tokens": float64(32000)}},
		"umans-glm-5.2": map[string]any{"display_name": "GLM", "capabilities": map[string]any{"context_window": float64(1000000), "max_completion_tokens": float64(131072), "supports_vision": "via-handoff"}},
		"umans-flash":   map[string]any{"display_name": "Flash", "capabilities": map[string]any{"context_window": float64(262144), "max_completion_tokens": float64(32768), "supports_vision": true}},
	}
	cat := ParseCatalog(raw)
	ctx, max, names, err := cat.SafeTier("umans-coder", "umans-glm-5.2", "umans-coder", "umans-flash")
	if err != nil {
		t.Fatal(err)
	}
	if ctx != 262144 {
		t.Fatalf("ctx=%d", ctx)
	}
	if max != 32767 {
		t.Fatalf("max=%d", max)
	}
	if names["umans-glm-5.2"] != "GLM" {
		t.Fatalf("names=%v", names)
	}
}

func TestSafeTierRejectsUnknown(t *testing.T) {
	cat := Catalog{Models: map[string]ModelInfo{}}
	if _, _, _, err := cat.SafeTier("missing", "umans-glm-5.2", "umans-coder", "umans-flash"); err == nil {
		t.Fatal("expected error")
	}
}
