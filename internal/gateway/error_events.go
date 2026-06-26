package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ErrorRecorder struct {
	dir     string
	maxAge  time.Duration
	maxSize int64
	mu      sync.Mutex
}

func NewErrorRecorder(cfg Config) (*ErrorRecorder, error) {
	if err := os.MkdirAll(cfg.ErrorEventDir, 0o700); err != nil {
		return nil, err
	}
	return &ErrorRecorder{
		dir:     cfg.ErrorEventDir,
		maxAge:  cfg.ErrorEventMaxAge,
		maxSize: cfg.ErrorEventMaxSize,
	}, nil
}

func (l *ErrorRecorder) Record(kind string, statusCode int, latency time.Duration, err error) {
	if l == nil || err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(time.Now())
	line, marshalErr := json.Marshal(map[string]any{
		"ts":             time.Now().UTC().Format(time.RFC3339Nano),
		"event":          normalizeEventKind(kind),
		"status_class":   statusClass(statusCode),
		"latency_bucket": latencyBucket(latency),
		"error_class":    classifyError(err),
	})
	if marshalErr != nil {
		return
	}
	path := filepath.Join(l.dir, time.Now().UTC().Format("20060102-15")+".jsonl")
	f, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if openErr != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func (l *ErrorRecorder) Cleanup() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cleanupLocked(time.Now())
}

func (l *ErrorRecorder) cleanupLocked(now time.Time) error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return err
	}
	type fileInfo struct {
		path string
		mod  time.Time
		size int64
	}
	var files []fileInfo
	var total int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(l.dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > l.maxAge {
			_ = os.Remove(path)
			continue
		}
		total += info.Size()
		files = append(files, fileInfo{path: path, mod: info.ModTime(), size: info.Size()})
	}
	if total <= l.maxSize {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= l.maxSize {
			return nil
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= f.size
	}
	return nil
}

func normalizeEventKind(kind string) string {
	switch kind {
	case "reject", "upstream_error", "upstream_status", "stream_error", "conversion_error", "catalog_error":
		return kind
	default:
		return "gateway_error"
	}
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "none"
	}
}

func latencyBucket(d time.Duration) string {
	switch {
	case d <= 0:
		return "unknown"
	case d < time.Second:
		return "<1s"
	case d < 5*time.Second:
		return "1-5s"
	case d < 30*time.Second:
		return "5-30s"
	case d < 2*time.Minute:
		return "30-120s"
	default:
		return ">=120s"
	}
}

func classifyError(err error) string {
	if err == nil {
		return "none"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context canceled") || strings.Contains(msg, "client disconnected"):
		return "cancelled"
	case strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "safe catalog output cap") || strings.Contains(msg, "max_tokens"):
		return "budget"
	case strings.Contains(msg, "upstream_status_4"):
		return "upstream_4xx"
	case strings.Contains(msg, "upstream_status_5"):
		return "upstream_5xx"
	case strings.Contains(msg, "json") || strings.Contains(msg, "decode") || strings.Contains(msg, "marshal"):
		return "serialization"
	default:
		return "other"
	}
}
