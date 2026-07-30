package security

import (
	"net/http"
	"testing"
)

func TestRedactHeaders(t *testing.T) {
	t.Parallel()
	headers := http.Header{
		"Authorization":       {"Bearer secret-value"},
		"Cookie":              {"session=secret"},
		"Idempotency-Key":     {"private-key"},
		"X-Webhook-Signature": {"sha256=secret"},
		"Content-Type":        {"application/json"},
	}

	redacted := RedactHeaders(headers)
	for _, name := range []string{"Authorization", "Cookie", "Idempotency-Key", "X-Webhook-Signature"} {
		if redacted[name] != "[REDACTED]" {
			t.Fatalf("%s was not redacted", name)
		}
	}
	if redacted["Content-Type"] != "application/json" {
		t.Fatal("non-sensitive header should remain visible")
	}
}
