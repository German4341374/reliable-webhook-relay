package security

import (
	"net/http"
	"strings"
)

// RedactHeaders returns log-safe header values.
func RedactHeaders(headers http.Header) map[string]string {
	redacted := make(map[string]string, len(headers))
	for name, values := range headers {
		if sensitiveHeader(name) {
			redacted[name] = "[REDACTED]"
			continue
		}
		redacted[name] = strings.Join(values, ",")
	}
	return redacted
}

func sensitiveHeader(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range []string{
		"authorization",
		"cookie",
		"idempotency-key",
		"token",
		"secret",
		"signature",
		"api-key",
		"apikey",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
