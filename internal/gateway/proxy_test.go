package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestService(t *testing.T, upstream http.HandlerFunc) (*Service, string) {
	t.Helper()
	ts := httptest.NewServer(upstream)
	t.Cleanup(ts.Close)
	cfg := DefaultConfig()
	cfg.UpstreamBaseURL = ts.URL + "/"
	cfg.ErrorEventDir = t.TempDir()
	svc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc, ts.URL
}

func TestMessagesRawProxyPreservesImageAndUsesXAPIKey(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Fatalf("x-api-key=%q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("authorization leaked=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "base64data") {
			t.Fatalf("image removed: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}]}`))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-glm-5.2","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"base64data"}}]}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMessagesNormalizesModelDurationSuffix(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if got := payload["model"]; got != "umans-glm-5.2" {
			t.Fatalf("model=%v body=%s", got, string(body))
		}
		_, _ = w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}]}`))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-glm-5.2[1m]","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "sk-test")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatProxySearchHeaderOverrideAndOpenAIAuth(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("bad auth %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Umans-Websearch-Provider") != "native" {
			t.Fatalf("websearch=%q", r.Header.Get("X-Umans-Websearch-Provider"))
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	})
	svc.cfg.SearchMode = SearchNative
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"umans-coder","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "sk-test")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestBudgetRejectsOverSafeMax(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"umans-coder":   map[string]any{"capabilities": map[string]any{"context_window": 1000, "max_completion_tokens": 10}},
				"umans-glm-5.2": map[string]any{"capabilities": map[string]any{"context_window": 1000, "max_completion_tokens": 20}},
				"umans-flash":   map[string]any{"capabilities": map[string]any{"context_window": 1000, "max_completion_tokens": 30}},
			})
			return
		}
		t.Fatal("upstream should not be called")
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-coder","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "sk-test")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != 400 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProxyLimitsConcurrentRequestsPerKey(t *testing.T) {
	started := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-releaseUpstream
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	svc.cfg.KeyConcurrency = 1
	svc.cfg.KeyQueueTimeout = 50 * time.Millisecond
	limiter, err := NewKeyLimiter(svc.cfg.KeyConcurrency, svc.cfg.KeyQueueTimeout)
	if err != nil {
		t.Fatal(err)
	}
	svc.limiter = limiter

	firstDone := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-glm-5.2","messages":[{"role":"user","content":"first"}]}`))
		req.Header.Set("x-api-key", "sk-same")
		rr := httptest.NewRecorder()
		svc.Routes().ServeHTTP(rr, req)
		firstDone <- rr.Code
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not reach upstream")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-glm-5.2","messages":[{"role":"user","content":"second"}]}`))
	req.Header.Set("x-api-key", "sk-same")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}

	close(releaseUpstream)
	if code := <-firstDone; code != http.StatusOK {
		t.Fatalf("first code=%d", code)
	}
}

func TestProxyRetriesTemporaryUnavailableAndHoldsKeySlot(t *testing.T) {
	var attempts atomic.Int32
	firstAttemptDone := make(chan struct{})
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		if attempt == 1 {
			close(firstAttemptDone)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"umans-glm-5.2[1m] is temporarily unavailable"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg_1","content":[{"type":"text","text":"ok"}]}`))
	})
	svc.cfg.KeyConcurrency = 1
	svc.cfg.KeyQueueTimeout = 25 * time.Millisecond
	svc.cfg.UpstreamRetryMax = 1
	svc.cfg.UpstreamRetryBase = 100 * time.Millisecond
	svc.cfg.UpstreamRetryCap = 100 * time.Millisecond
	limiter, err := NewKeyLimiter(svc.cfg.KeyConcurrency, svc.cfg.KeyQueueTimeout)
	if err != nil {
		t.Fatal(err)
	}
	svc.limiter = limiter

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-glm-5.2","messages":[{"role":"user","content":"first"}]}`))
		req.Header.Set("x-api-key", "sk-same")
		rr := httptest.NewRecorder()
		svc.Routes().ServeHTTP(rr, req)
		firstDone <- rr
	}()

	select {
	case <-firstAttemptDone:
	case <-time.After(time.Second):
		t.Fatal("first upstream attempt did not run")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"umans-glm-5.2","messages":[{"role":"user","content":"second"}]}`))
	req.Header.Set("x-api-key", "sk-same")
	rr := httptest.NewRecorder()
	svc.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second code=%d body=%s", rr.Code, rr.Body.String())
	}

	first := <-firstDone
	if first.Code != http.StatusOK {
		t.Fatalf("first code=%d body=%s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-Umans-Gateway-Retry-Attempts"); got != "1" {
		t.Fatalf("retry attempts header=%q", got)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("upstream attempts=%d", got)
	}
}
