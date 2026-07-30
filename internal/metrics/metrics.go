package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type Snapshot struct {
	Received  uint64
	Delivered uint64
	Failed    uint64
	Retry     uint64
}

type Counters struct {
	received  atomic.Uint64
	delivered atomic.Uint64
	failed    atomic.Uint64
	retry     atomic.Uint64
}

func (c *Counters) IncReceived() {
	c.received.Add(1)
}

func (c *Counters) IncDelivered() {
	c.delivered.Add(1)
}

func (c *Counters) IncFailed() {
	c.failed.Add(1)
}

func (c *Counters) IncRetry() {
	c.retry.Add(1)
}

func (c *Counters) Snapshot() Snapshot {
	return Snapshot{
		Received:  c.received.Load(),
		Delivered: c.delivered.Load(),
		Failed:    c.failed.Load(),
		Retry:     c.retry.Load(),
	}
}

func (c *Counters) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	snapshot := c.Snapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `# HELP received_total Persisted webhook deliveries.
# TYPE received_total counter
received_total %d
# HELP delivered_total Successfully delivered webhooks.
# TYPE delivered_total counter
delivered_total %d
# HELP failed_total Failed delivery attempts.
# TYPE failed_total counter
failed_total %d
# HELP retry_total Scheduled retries.
# TYPE retry_total counter
retry_total %d
`, snapshot.Received, snapshot.Delivered, snapshot.Failed, snapshot.Retry)
}
