package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWSCancel(t *testing.T) {
	svc, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(2 * time.Second)
	})
	ts := httptest.NewServer(svc.Routes())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	req := map[string]any{"type": "request", "id": "r1", "endpoint": "/v1/chat/completions", "headers": map[string]string{"Authorization": "Bearer sk-test"}, "body": map[string]any{"model": "umans-coder", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}}
	data, _ := json.Marshal(req)
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	cancelFrame, _ := json.Marshal(map[string]any{"type": "cancel", "id": "r1"})
	if err := c.Write(ctx, websocket.MessageText, cancelFrame); err != nil {
		t.Fatal(err)
	}
	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(msg), `"cancelled":true`) {
			return
		}
	}
}
