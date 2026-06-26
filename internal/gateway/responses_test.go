package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseToChatPreservesToolsReasoningAndImage(t *testing.T) {
	in := []byte(`{
	  "model":"umans-coder",
	  "instructions":"be terse",
	  "reasoning_effort":"medium",
	  "max_output_tokens":123,
	  "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],
	  "input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"what is this"},{"type":"input_image","image_url":"data:image/jpeg;base64,abc"}]}]
	}`)
	out, err := responseToChatBody(in)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"reasoning_effort", "max_tokens", "image_url", "data:image/jpeg;base64,abc", "tools"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestResponsesEndpointConvertsBack(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","model":"umans-coder","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"total_tokens":3}}`))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"umans-coder","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["object"] != "response" || got["output_text"] != "hello" {
		t.Fatalf("bad response %#v", got)
	}
}
