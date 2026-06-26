package gateway

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrMissingAPIKey = errors.New("missing API key")
	ErrKeyMismatch   = errors.New("authorization and x-api-key differ")
)

type AuthInfo struct {
	Key    string
	Source string
}

func ExtractAuth(h http.Header) (AuthInfo, error) {
	bearer := bearerToken(headerGetCI(h, "Authorization"))
	xKey := strings.TrimSpace(headerGetCI(h, "x-api-key"))
	if bearer != "" && xKey != "" && bearer != xKey {
		return AuthInfo{}, ErrKeyMismatch
	}
	switch {
	case xKey != "":
		return AuthInfo{Key: xKey, Source: "x-api-key"}, nil
	case bearer != "":
		return AuthInfo{Key: bearer, Source: "authorization"}, nil
	default:
		return AuthInfo{}, ErrMissingAPIKey
	}
}

func headerGetCI(h http.Header, name string) string {
	if v := h.Get(name); v != "" {
		return v
	}
	for k, vals := range h {
		if strings.EqualFold(k, name) && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

func bearerToken(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	parts := strings.Fields(v)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func RedactKey(key string, preview int) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if preview <= 0 {
		preview = 6
	}
	if len(key) <= preview {
		return key + "..."
	}
	return key[:preview] + "..."
}
