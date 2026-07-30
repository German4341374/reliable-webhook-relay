package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandler(t *testing.T) {
	t.Parallel()
	counters := &Counters{}
	counters.IncReceived()
	counters.IncDelivered()
	counters.IncFailed()
	counters.IncRetry()

	recorder := httptest.NewRecorder()
	counters.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	for _, line := range []string{
		"received_total 1",
		"delivered_total 1",
		"failed_total 1",
		"retry_total 1",
	} {
		if !strings.Contains(recorder.Body.String(), line) {
			t.Fatalf("metrics output does not contain %q", line)
		}
	}
}
