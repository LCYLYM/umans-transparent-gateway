package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type ModelInfo struct {
	ID                   string
	DisplayName          string
	ContextWindow        int
	MaxCompletionTokens  int
	RecommendedMaxTokens int
	SupportsVision       any
	Deprecated           bool
	Raw                  map[string]any
}

type Catalog struct {
	Models   map[string]ModelInfo
	Fetched  time.Time
	Fallback bool
}

type CatalogService struct {
	client   *http.Client
	baseURL  string
	ttl      time.Duration
	recorder *ErrorRecorder
	mu       sync.Mutex
	byKey    map[string]Catalog
}

func NewCatalogService(client *http.Client, baseURL string, ttl time.Duration, recorder *ErrorRecorder) *CatalogService {
	return &CatalogService{
		client:   client,
		baseURL:  baseURL,
		ttl:      ttl,
		recorder: recorder,
		byKey:    map[string]Catalog{},
	}
}

func (s *CatalogService) Get(ctx context.Context, key string) (Catalog, bool, error) {
	s.mu.Lock()
	if c, ok := s.byKey[key]; ok && time.Since(c.Fetched) < s.ttl {
		s.mu.Unlock()
		return c, true, nil
	}
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(s.baseURL, "/v1/models/info"), nil)
	if err != nil {
		return Catalog{}, false, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("User-Agent", "umans-transparent-gateway/1")
	resp, err := s.client.Do(req)
	if err != nil {
		s.recordError("catalog_error", 0, 0, err)
		return Catalog{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("models/info upstream status %d", resp.StatusCode)
		s.recordError("catalog_error", resp.StatusCode, 0, err)
		return Catalog{}, false, err
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		s.recordError("catalog_error", resp.StatusCode, 0, err)
		return Catalog{}, false, err
	}
	cat := ParseCatalog(raw)
	cat.Fetched = time.Now()
	s.mu.Lock()
	s.byKey[key] = cat
	s.mu.Unlock()
	return cat, false, nil
}

func ParseCatalog(raw map[string]any) Catalog {
	models := make(map[string]ModelInfo, len(raw))
	for id, v := range raw {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		capObj, _ := obj["capabilities"].(map[string]any)
		mi := ModelInfo{
			ID:                   id,
			DisplayName:          stringValue(obj["display_name"], id),
			ContextWindow:        intValue(capObj["context_window"], 262144),
			MaxCompletionTokens:  intValue(capObj["max_completion_tokens"], 0),
			RecommendedMaxTokens: intValue(capObj["recommended_max_tokens"], 0),
			SupportsVision:       capObj["supports_vision"],
			Deprecated:           obj["deprecation"] != nil,
			Raw:                  obj,
		}
		models[id] = mi
	}
	return Catalog{Models: models}
}

func (c Catalog) SafeTier(defaultModel, opus, sonnet, haiku string) (contextWindow int, maxOutput int, names map[string]string, err error) {
	ids := dedupe([]string{defaultModel, opus, sonnet, haiku})
	if len(ids) == 0 {
		return 0, 0, nil, errors.New("no active model ids")
	}
	names = map[string]string{}
	for _, id := range ids {
		m, ok := c.Models[id]
		if !ok {
			return 0, 0, nil, fmt.Errorf("active model %q missing from catalog", id)
		}
		ctx := m.ContextWindow
		if ctx <= 0 {
			ctx = 262144
		}
		cap := m.MaxCompletionTokens
		if cap <= 0 {
			return 0, 0, nil, fmt.Errorf("active model %q missing max_completion_tokens", id)
		}
		if contextWindow == 0 || ctx < contextWindow {
			contextWindow = ctx
		}
		if maxOutput == 0 || cap-1 < maxOutput {
			maxOutput = cap - 1
		}
		names[id] = m.DisplayName
	}
	return contextWindow, maxOutput, names, nil
}

func (s *CatalogService) recordError(kind string, statusCode int, latency time.Duration, err error) {
	if s.recorder != nil {
		s.recorder.Record(kind, statusCode, latency, err)
	}
}

func intValue(v any, fallback int) int {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return int(i)
		}
	}
	return fallback
}

func stringValue(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func joinURL(base, path string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + path
	}
	u.Path = stringsTrimRightSlash(u.Path) + path
	u.RawQuery = ""
	return u.String()
}

func stringsTrimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
