package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type Service struct {
	cfg      Config
	client   *http.Client
	recorder *ErrorRecorder
	catalog  *CatalogService
	limiter  *KeyLimiter
}

func New(cfg Config) (*Service, error) {
	recorder, err := NewErrorRecorder(cfg)
	if err != nil {
		return nil, err
	}
	limiter, err := NewKeyLimiter(cfg.KeyConcurrency, cfg.KeyQueueTimeout)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 0}
	return &Service{
		cfg:      cfg,
		client:   client,
		recorder: recorder,
		catalog:  NewCatalogService(client, cfg.UpstreamBaseURL, cfg.CatalogTTL, recorder),
		limiter:  limiter,
	}, nil
}

func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/client/claude-env", s.handleClaudeEnv)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleMessagesCountTokens)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/v1/models/info", s.handleModelsInfo)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/usage", s.handleUsage)
	return mux
}

func (s *Service) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"upstream": s.cfg.UpstreamBaseURL,
		"version":  "dev",
	})
}

func (s *Service) handleClaudeEnv(w http.ResponseWriter, r *http.Request) {
	base := externalBaseURL(r)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "ANTHROPIC_BASE_URL=%s\n", base)
	fmt.Fprintln(w, "ANTHROPIC_AUTH_TOKEN=<user key>")
	fmt.Fprintf(w, "ANTHROPIC_DEFAULT_OPUS_MODEL=%s\n", s.cfg.OpusModel)
	fmt.Fprintf(w, "ANTHROPIC_DEFAULT_SONNET_MODEL=%s\n", s.cfg.SonnetModel)
	fmt.Fprintf(w, "ANTHROPIC_DEFAULT_HAIKU_MODEL=%s\n", s.cfg.HaikuModel)
	fmt.Fprintln(w, "DISABLE_NON_ESSENTIAL_MODEL_CALLS=1")
	fmt.Fprintln(w, "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=90")
	fmt.Fprintln(w, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	fmt.Fprintln(w, "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING=1")
	fmt.Fprintln(w, "ENABLE_TOOL_SEARCH=false")
	fmt.Fprintln(w, "CLAUDE_CODE_ATTRIBUTION_HEADER=0")
}

func (s *Service) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, proxyOptions{Endpoint: "/v1/messages", AuthStyle: authAnthropic, BudgetField: "max_tokens"})
}

func (s *Service) handleMessagesCountTokens(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, proxyOptions{Endpoint: "/v1/messages/count_tokens", AuthStyle: authAnthropic})
}

func (s *Service) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, proxyOptions{Endpoint: "/v1/chat/completions", AuthStyle: authOpenAI, BudgetField: "max_tokens", AltBudgetField: "max_completion_tokens"})
}

func (s *Service) handleModelsInfo(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, proxyOptions{Endpoint: "/v1/models/info", AuthStyle: authAnthropic, AllowGET: true})
}

func (s *Service) handleModels(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, proxyOptions{Endpoint: "/v1/models", AuthStyle: authOpenAI, AllowGET: true})
}

func (s *Service) handleUsage(w http.ResponseWriter, r *http.Request) {
	s.proxyJSON(w, r, proxyOptions{Endpoint: "/v1/usage", AuthStyle: authOpenAI, AllowGET: true})
}

type authStyle int

const (
	authAnthropic authStyle = iota
	authOpenAI
)

type proxyOptions struct {
	Endpoint       string
	AuthStyle      authStyle
	BudgetField    string
	AltBudgetField string
	AllowGET       bool
}

func (s *Service) proxyJSON(w http.ResponseWriter, r *http.Request, opts proxyOptions) {
	start := time.Now()
	if r.Method != http.MethodPost && !(opts.AllowGET && r.Method == http.MethodGet) {
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
	body = normalizeRequestJSON(body)

	modified, budgetNote, err := s.applyBudgetPolicy(r.Context(), auth.Key, body, opts.BudgetField, opts.AltBudgetField)
	if err != nil {
		s.recordError("reject", http.StatusBadRequest, time.Since(start), err)
		writeError(w, http.StatusBadRequest, "max_output_exceeds_catalog", err.Error())
		return
	}
	if modified != nil {
		body = modified
		w.Header().Set("X-Umans-Gateway-Budget", budgetNote)
	}

	resp, attempts, err := s.doUpstreamWithRetry(r.Context(), func(ctx context.Context) (*http.Request, error) {
		upReq, err := http.NewRequestWithContext(ctx, r.Method, joinURL(s.cfg.UpstreamBaseURL, opts.Endpoint), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		copyHeaders(upReq.Header, r.Header)
		applyOutboundAuth(upReq.Header, auth.Key, opts.AuthStyle)
		s.applySearchHeader(upReq.Header, r.Header)
		upReq.Header.Set("User-Agent", "umans-transparent-gateway/1")
		if len(body) > 0 && upReq.Header.Get("Content-Type") == "" {
			upReq.Header.Set("Content-Type", "application/json")
		}
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

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, copyErr := streamCopy(w, resp.Body)
	errForLog := copyErr
	if resp.StatusCode >= 400 && errForLog == nil {
		errForLog = fmt.Errorf("upstream_status_%d", resp.StatusCode)
	}
	if errForLog != nil {
		s.recordError("upstream_status", resp.StatusCode, time.Since(start), errForLog)
	}
}

func (s *Service) applyBudgetPolicy(ctx context.Context, key string, body []byte, field, altField string) ([]byte, string, error) {
	if len(body) == 0 || field == "" {
		return nil, "", nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", nil
	}
	requested := intValue(payload[field], 0)
	usedField := field
	if requested == 0 && altField != "" {
		requested = intValue(payload[altField], 0)
		usedField = altField
	}
	if requested == 0 {
		return nil, "", nil
	}
	cat, _, err := s.catalog.Get(ctx, key)
	if err != nil {
		return nil, "catalog_unavailable_passthrough", nil
	}
	model := stringValue(payload["model"], s.cfg.DefaultModel)
	_, maxOutput, _, err := cat.SafeTier(model, s.cfg.OpusModel, s.cfg.SonnetModel, s.cfg.HaikuModel)
	if err != nil {
		return nil, "catalog_incomplete_passthrough", nil
	}
	if requested <= maxOutput {
		return nil, fmt.Sprintf("ok:%s=%d<=%d", usedField, requested, maxOutput), nil
	}
	if s.cfg.BudgetPolicy == BudgetClampVisible {
		payload[usedField] = maxOutput
		next, err := json.Marshal(payload)
		if err != nil {
			return nil, "", err
		}
		return next, fmt.Sprintf("clamped:%s=%d->%d", usedField, requested, maxOutput), nil
	}
	return nil, fmt.Sprintf("reject:%s=%d>%d", usedField, requested, maxOutput), fmt.Errorf("%s %d exceeds safe catalog output cap %d", usedField, requested, maxOutput)
}

func (s *Service) applySearchHeader(dst http.Header, inbound http.Header) {
	const name = "X-Umans-Websearch-Provider"
	switch s.cfg.SearchMode {
	case SearchNative:
		dst.Set(name, "native")
	case SearchExa:
		dst.Set(name, "exa")
	case SearchNone:
		dst.Set(name, "none")
	case SearchAuto:
		if v := inbound.Get(name); v != "" {
			dst.Set(name, v)
		} else {
			dst.Del(name)
		}
	}
}

func (s *Service) recordError(kind string, statusCode int, latency time.Duration, err error) {
	if s.recorder != nil {
		s.recorder.Record(kind, statusCode, latency, err)
	}
}

func (s *Service) acquireKeySlot(ctx context.Context, key string) (func(), error) {
	if s.limiter == nil {
		return func() {}, nil
	}
	return s.limiter.Acquire(ctx, key)
}

func (s *Service) writeConcurrencyError(w http.ResponseWriter, start time.Time, err error) {
	switch {
	case errors.Is(err, ErrConcurrencyQueueTimeout):
		s.recordError("concurrency_queue_timeout", http.StatusTooManyRequests, time.Since(start), err)
		writeError(w, http.StatusTooManyRequests, "concurrency_queue_timeout", "per-key concurrency queue timeout")
	case errors.Is(err, ErrConcurrencyWaitCanceled):
		s.recordError("concurrency_wait_canceled", 499, time.Since(start), err)
		writeError(w, http.StatusRequestTimeout, "concurrency_wait_canceled", "request canceled while waiting for concurrency slot")
	default:
		s.recordError("concurrency_limiter_error", http.StatusInternalServerError, time.Since(start), err)
		writeError(w, http.StatusInternalServerError, "concurrency_limiter_error", "failed to acquire concurrency slot")
	}
}

func bodyHasBool(body []byte, key string) bool {
	var payload map[string]any
	return json.Unmarshal(body, &payload) == nil && payload[key] == true
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrMissingAPIKey):
		writeError(w, http.StatusUnauthorized, "missing_api_key", "missing API key")
	case errors.Is(err, ErrKeyMismatch):
		writeError(w, http.StatusBadRequest, "api_key_mismatch", "authorization and x-api-key differ")
	default:
		writeError(w, http.StatusUnauthorized, "auth_error", "authentication failed")
	}
}

func writeError(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": typ, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		if isHopHeader(k) || strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "x-api-key") {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vals := range src {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func isHopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "content-length":
		return true
	default:
		return false
	}
}

func applyOutboundAuth(h http.Header, key string, style authStyle) {
	h.Del("Authorization")
	h.Del("x-api-key")
	if style == authAnthropic {
		h.Set("x-api-key", key)
		return
	}
	h.Set("Authorization", "Bearer "+key)
}

func streamCopy(w http.ResponseWriter, r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	flusher, _ := w.(http.Flusher)
	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := w.Write(buf[:nr])
			total += int64(nw)
			if flusher != nil {
				flusher.Flush()
			}
			if ew != nil {
				return total, ew
			}
			if nr != nw {
				return total, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return total, nil
			}
			return total, er
		}
	}
}

func externalBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = strings.Split(xf, ",")[0]
	}
	host := r.Host
	if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
		host = strings.Split(xh, ",")[0]
	}
	return scheme + "://" + strings.TrimSpace(host)
}

func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, RedactKey(secret, 6))
}

type wsRequest struct {
	Type     string            `json:"type"`
	ID       string            `json:"id"`
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
	Body     json.RawMessage   `json:"body"`
}

func (s *Service) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()
	cancels := map[string]context.CancelFunc{}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req wsRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeWS(ctx, c, map[string]any{"type": "error", "error": map[string]any{"type": "bad_json", "message": "invalid json"}})
			continue
		}
		if req.Type == "cancel" {
			if cancel := cancels[req.ID]; cancel != nil {
				cancel()
				delete(cancels, req.ID)
				writeWS(ctx, c, map[string]any{"type": "done", "id": req.ID, "cancelled": true})
			}
			continue
		}
		if req.Type != "request" || req.ID == "" {
			writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "error": map[string]any{"type": "bad_request", "message": "request id/type required"}})
			continue
		}
		childCtx, cancel := context.WithCancel(ctx)
		cancels[req.ID] = cancel
		go func(req wsRequest) {
			defer delete(cancels, req.ID)
			defer cancel()
			s.runWSRequest(childCtx, c, req)
		}(req)
	}
}

func (s *Service) runWSRequest(ctx context.Context, c *websocket.Conn, req wsRequest) {
	start := time.Now()
	if req.Endpoint != "/v1/messages" && req.Endpoint != "/v1/chat/completions" && req.Endpoint != "/v1/responses" {
		writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "error": map[string]any{"type": "unsupported_endpoint", "message": "unsupported endpoint"}})
		return
	}
	h := http.Header{}
	for k, v := range req.Headers {
		h.Set(k, v)
	}
	auth, err := ExtractAuth(h)
	if err != nil {
		writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "error": map[string]any{"type": "auth_error", "message": err.Error()}})
		return
	}
	release, err := s.acquireKeySlot(ctx, auth.Key)
	if err != nil {
		s.recordError("concurrency_queue_timeout", http.StatusTooManyRequests, time.Since(start), err)
		writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "status": http.StatusTooManyRequests, "error": map[string]any{"type": "concurrency_queue_timeout", "message": "per-key concurrency queue timeout"}})
		return
	}
	defer release()

	body := append([]byte(nil), req.Body...)
	var payload map[string]any
	body = normalizeRequestJSON(body)
	_ = json.Unmarshal(body, &payload)
	payload["stream"] = true
	body, _ = json.Marshal(payload)

	upEndpoint := req.Endpoint
	style := authAnthropic
	if req.Endpoint == "/v1/chat/completions" {
		style = authOpenAI
	}
	if req.Endpoint == "/v1/responses" {
		chatBody, err := responseToChatBody(body)
		if err != nil {
			writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "error": map[string]any{"type": "responses_convert_failed", "message": err.Error()}})
			return
		}
		body = chatBody
		upEndpoint = "/v1/chat/completions"
		style = authOpenAI
	}
	resp, _, err := s.doUpstreamWithRetry(ctx, func(ctx context.Context) (*http.Request, error) {
		upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(s.cfg.UpstreamBaseURL, upEndpoint), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		copyHeaders(upReq.Header, h)
		applyOutboundAuth(upReq.Header, auth.Key, style)
		s.applySearchHeader(upReq.Header, h)
		upReq.Header.Set("Content-Type", "application/json")
		return upReq, nil
	})
	if err != nil {
		writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "error": map[string]any{"type": "upstream_request_failed", "message": redactSecret(err.Error(), auth.Key)}})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "status": resp.StatusCode, "error": map[string]any{"type": "upstream_error", "message": redactSecret(string(body), auth.Key)}})
		return
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		writeWS(ctx, c, map[string]any{"type": "event", "id": req.ID, "event": line})
	}
	if err := scanner.Err(); err != nil {
		writeWS(ctx, c, map[string]any{"type": "error", "id": req.ID, "error": map[string]any{"type": "stream_error", "message": err.Error()}})
		return
	}
	writeWS(ctx, c, map[string]any{"type": "done", "id": req.ID})
}

func writeWS(ctx context.Context, c *websocket.Conn, v any) {
	data, _ := json.Marshal(v)
	_ = c.Write(ctx, websocket.MessageText, data)
}
