package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

const retryDecisionBodyLimit = 1024 * 1024

func (s *Service) doUpstreamWithRetry(ctx context.Context, build func(context.Context) (*http.Request, error)) (*http.Response, int, error) {
	attempts := s.cfg.UpstreamRetryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := build(ctx)
		if err != nil {
			return nil, attempt + 1, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < attempts && waitRetry(ctx, s.retryDelay(attempt)) {
				continue
			}
			return nil, attempt + 1, err
		}

		if resp.StatusCode < 400 {
			return resp, attempt + 1, nil
		}

		body, readErr := readAndRestoreBody(resp)
		if readErr != nil {
			return resp, attempt + 1, nil
		}
		if attempt+1 < attempts && shouldRetryUpstream(resp.StatusCode, body) {
			resp.Body.Close()
			if waitRetry(ctx, s.retryDelay(attempt)) {
				continue
			}
		}
		return resp, attempt + 1, nil
	}
	return nil, attempts, lastErr
}

func readAndRestoreBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, retryDecisionBodyLimit))
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return body, nil
}

func shouldRetryUpstream(status int, body []byte) bool {
	if retryableStatus(status) {
		return true
	}
	msg := strings.ToLower(string(body))
	return strings.Contains(msg, "temporarily unavailable") ||
		strings.Contains(msg, "temporary unavailable") ||
		strings.Contains(msg, "try again") ||
		strings.Contains(msg, "overloaded") ||
		strings.Contains(msg, "no available claude accounts")
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	default:
		return status >= 500 && status != http.StatusNotImplemented
	}
}

func (s *Service) retryDelay(attempt int) time.Duration {
	delay := s.cfg.UpstreamRetryBase
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= s.cfg.UpstreamRetryCap {
			return s.cfg.UpstreamRetryCap
		}
	}
	if delay > s.cfg.UpstreamRetryCap {
		return s.cfg.UpstreamRetryCap
	}
	return delay
}

func waitRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
