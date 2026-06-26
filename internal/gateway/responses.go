package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Service) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	auth, err := ExtractAuth(r.Header)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	release, err := s.acquireKeySlot(r.Context(), auth.Key)
	if err != nil {
		s.writeConcurrencyError(w, start, err)
		return
	}
	defer release()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_body_failed", "failed to read request body")
		return
	}
	defer r.Body.Close()
	body = normalizeModelInJSON(body)
	chatBody, err := responseToChatBody(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "responses_convert_failed", err.Error())
		return
	}

	resp, attempts, err := s.doUpstreamWithRetry(r.Context(), func(ctx context.Context) (*http.Request, error) {
		upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(s.cfg.UpstreamBaseURL, "/v1/chat/completions"), bytes.NewReader(chatBody))
		if err != nil {
			return nil, err
		}
		copyHeaders(upReq.Header, r.Header)
		applyOutboundAuth(upReq.Header, auth.Key, authOpenAI)
		s.applySearchHeader(upReq.Header, r.Header)
		upReq.Header.Set("Content-Type", "application/json")
		upReq.Header.Set("User-Agent", "umans-transparent-gateway/1")
		return upReq, nil
	})
	if err != nil {
		s.recordError("upstream_error", 0, time.Since(start), err)
		writeError(w, http.StatusBadGateway, "upstream_request_failed", err.Error())
		return
	}
	defer resp.Body.Close()
	if attempts > 1 {
		w.Header().Set("X-Umans-Gateway-Retry-Attempts", fmt.Sprintf("%d", attempts-1))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = streamCopy(w, resp.Body)
		s.recordError("upstream_status", resp.StatusCode, time.Since(start), fmt.Errorf("upstream_status_%d", resp.StatusCode))
		return
	}
	if bodyHasBool(body, "stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, err := convertChatStreamToResponses(w, resp.Body)
		if err != nil {
			s.recordError("stream_error", 0, time.Since(start), err)
		}
		return
	}
	var chat map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		s.recordError("conversion_error", http.StatusBadGateway, time.Since(start), err)
		writeError(w, http.StatusBadGateway, "decode_upstream_failed", err.Error())
		return
	}
	response := chatToResponse(chat)
	writeJSON(w, http.StatusOK, response)
}

func responseToChatBody(body []byte) ([]byte, error) {
	var in map[string]any
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range []string{"model", "stream", "temperature", "top_p", "tools", "tool_choice", "parallel_tool_calls", "reasoning_effort"} {
		if v, ok := in[k]; ok {
			out[k] = v
		}
	}
	if v, ok := in["max_output_tokens"]; ok {
		out["max_tokens"] = v
	}
	if v, ok := in["max_completion_tokens"]; ok {
		out["max_completion_tokens"] = v
	}
	if v, ok := in["instructions"]; ok {
		out["messages"] = append([]any{map[string]any{"role": "system", "content": v}}, responsesInputToChatMessages(in["input"])...)
	} else {
		out["messages"] = responsesInputToChatMessages(in["input"])
	}
	if _, ok := out["model"]; !ok {
		return nil, fmt.Errorf("model is required")
	}
	if msgs, _ := out["messages"].([]any); len(msgs) == 0 {
		return nil, fmt.Errorf("input is required")
	}
	return json.Marshal(out)
}

func responsesInputToChatMessages(v any) []any {
	switch x := v.(type) {
	case string:
		return []any{map[string]any{"role": "user", "content": x}}
	case []any:
		var msgs []any
		for _, item := range x {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := obj["type"].(string)
			switch typ {
			case "message":
				role := stringValue(obj["role"], "user")
				msgs = append(msgs, map[string]any{"role": role, "content": responsesContentToChatContent(obj["content"])})
			case "function_call_output":
				msgs = append(msgs, map[string]any{
					"role":         "tool",
					"tool_call_id": stringValue(obj["call_id"], stringValue(obj["id"], "")),
					"content":      obj["output"],
				})
			default:
				if _, ok := obj["role"]; ok {
					msgs = append(msgs, map[string]any{"role": stringValue(obj["role"], "user"), "content": responsesContentToChatContent(obj["content"])})
				}
			}
		}
		return msgs
	default:
		return nil
	}
}

func responsesContentToChatContent(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	var out []any
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		typ, _ := obj["type"].(string)
		switch typ {
		case "input_text", "output_text":
			out = append(out, map[string]any{"type": "text", "text": obj["text"]})
		case "input_image":
			if url, ok := obj["image_url"]; ok {
				out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			} else {
				out = append(out, obj)
			}
		default:
			out = append(out, obj)
		}
	}
	return out
}

func chatToResponse(chat map[string]any) map[string]any {
	id := stringValue(chat["id"], "resp_"+time.Now().UTC().Format("20060102150405"))
	model := stringValue(chat["model"], "")
	choices, _ := chat["choices"].([]any)
	var output []any
	var outputText strings.Builder
	for _, ch := range choices {
		choice, _ := ch.(map[string]any)
		msg, _ := choice["message"].(map[string]any)
		content := msg["content"]
		if s, ok := content.(string); ok && s != "" {
			outputText.WriteString(s)
			output = append(output, map[string]any{
				"type":    "message",
				"id":      id + "_msg",
				"role":    stringValue(msg["role"], "assistant"),
				"content": []any{map[string]any{"type": "output_text", "text": s}},
			})
		}
		if calls, ok := msg["tool_calls"].([]any); ok {
			for _, call := range calls {
				callObj, _ := call.(map[string]any)
				fn, _ := callObj["function"].(map[string]any)
				output = append(output, map[string]any{
					"type":      "function_call",
					"id":        callObj["id"],
					"call_id":   callObj["id"],
					"name":      fn["name"],
					"arguments": fn["arguments"],
				})
			}
		}
	}
	return map[string]any{
		"id":                  id,
		"object":              "response",
		"created_at":          time.Now().Unix(),
		"status":              "completed",
		"model":               model,
		"output":              output,
		"output_text":         outputText.String(),
		"usage":               chat["usage"],
		"parallel_tool_calls": true,
	}
}

func convertChatStreamToResponses(w http.ResponseWriter, r io.Reader) (int64, error) {
	flusher, _ := w.(http.Flusher)
	var written int64
	emit := func(event string, data any) {
		payload, _ := json.Marshal(data)
		n, _ := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		written += int64(n)
		if flusher != nil {
			flusher.Flush()
		}
	}
	id := "resp_" + time.Now().UTC().Format("20060102150405")
	emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "status": "in_progress"}})
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed"}})
			n, _ := fmt.Fprint(w, "data: [DONE]\n\n")
			written += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
			return written, nil
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		for _, ch := range asArray(chunk["choices"]) {
			choice, _ := ch.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if text, ok := delta["content"].(string); ok && text != "" {
				emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": text})
			}
			for _, call := range asArray(delta["tool_calls"]) {
				emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "delta": call})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return written, err
	}
	emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed"}})
	return written, nil
}

func asArray(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
