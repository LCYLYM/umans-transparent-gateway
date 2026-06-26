package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestExtractAuth(t *testing.T) {
	tests := []struct {
		name    string
		header  http.Header
		wantKey string
		wantErr error
	}{
		{"bearer", http.Header{"Authorization": {"Bearer sk-test"}}, "sk-test", nil},
		{"x-api-key", http.Header{"x-api-key": {"sk-test"}}, "sk-test", nil},
		{"both same", http.Header{"Authorization": {"Bearer sk-test"}, "x-api-key": {"sk-test"}}, "sk-test", nil},
		{"mismatch", http.Header{"Authorization": {"Bearer sk-a"}, "x-api-key": {"sk-b"}}, "", ErrKeyMismatch},
		{"missing", http.Header{}, "", ErrMissingAPIKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractAuth(tt.header)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("err=%v want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Key != tt.wantKey {
				t.Fatalf("key=%q want %q", got.Key, tt.wantKey)
			}
		})
	}
}

func TestRedactKey(t *testing.T) {
	raw := "sk-abcdef-secret"
	got := RedactKey(raw, 6)
	if got != "sk-abc..." {
		t.Fatalf("redacted=%q", got)
	}
	if strings.Contains(got, "secret") {
		t.Fatal("redaction leaked suffix")
	}
}
