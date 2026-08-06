package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/metrics"
	"github.com/German4341374/reliable-webhook-relay/internal/model"
	"github.com/German4341374/reliable-webhook-relay/internal/security"
	"github.com/German4341374/reliable-webhook-relay/internal/storage"
	"github.com/German4341374/reliable-webhook-relay/internal/worker"
)

const testSecret = "integration-test-secret-32-characters" // gitleaks:allow -- test fixture

func TestRelayPersistsRetriesAndDelivers(t *testing.T) {
	var receiverAttempts atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !security.VerifySignature(testSecret, payload, r.Header.Get("X-Webhook-Signature")) {
			t.Error("relay sent an invalid signature")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if receiverAttempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)

	store, err := storage.Open(context.Background(), t.TempDir()+"/relay.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	counters := &metrics.Counters{}
	channels := map[string]model.Channel{
		"demo": {
			Name:        "demo",
			TargetURL:   receiver.URL,
			Secret:      testSecret,
			Timeout:     time.Second,
			MaxAttempts: 3,
		},
	}
	deliveryWorker := worker.New(
		store,
		channels,
		security.NewHTTPClient(true, logger),
		counters,
		logger,
		5*time.Millisecond,
		5*time.Millisecond,
		20*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	deliveryWorker.Start(ctx)
	t.Cleanup(func() {
		cancel()
		deliveryWorker.Wait()
	})
	waitFor(t, time.Second, deliveryWorker.Running)

	server := httptest.NewServer(New(store, channels, deliveryWorker, counters, logger).Handler())
	t.Cleanup(server.Close)

	payload := []byte(`{"event":"created"}`)
	first := sendWebhook(t, server.URL, payload, "event-123")
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first request status = %d, want 202", first.StatusCode)
	}
	var firstResponse struct {
		Delivery  model.Delivery `json:"delivery"`
		Duplicate bool           `json:"duplicate"`
	}
	decodeAndClose(t, first, &firstResponse)
	if firstResponse.Duplicate {
		t.Fatal("first request was reported as duplicate")
	}

	waitFor(t, 2*time.Second, func() bool {
		delivery, loadErr := store.GetDelivery(context.Background(), firstResponse.Delivery.ID)
		return loadErr == nil && delivery.Status == model.StatusDelivered && delivery.Attempts == 2
	})

	duplicate := sendWebhook(t, server.URL, payload, "event-123")
	if duplicate.StatusCode != http.StatusOK {
		t.Fatalf("duplicate request status = %d, want 200", duplicate.StatusCode)
	}
	var duplicateResponse struct {
		Delivery  model.Delivery `json:"delivery"`
		Duplicate bool           `json:"duplicate"`
	}
	decodeAndClose(t, duplicate, &duplicateResponse)
	if !duplicateResponse.Duplicate || duplicateResponse.Delivery.ID != firstResponse.Delivery.ID {
		t.Fatal("duplicate response did not reference the original delivery")
	}

	snapshot := counters.Snapshot()
	if snapshot.Received != 1 || snapshot.Delivered != 1 || snapshot.Failed != 1 || snapshot.Retry != 1 {
		t.Fatalf("unexpected metrics snapshot: %+v", snapshot)
	}
}

func TestWebhookRejectsInvalidSignatureAndOversizedPayload(t *testing.T) {
	store, err := storage.Open(context.Background(), t.TempDir()+"/relay.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	counters := &metrics.Counters{}
	channels := map[string]model.Channel{
		"demo": {Name: "demo", Secret: testSecret},
	}
	control := &fakeWorker{running: true}
	server := httptest.NewServer(New(store, channels, control, counters, logger).Handler())
	t.Cleanup(server.Close)

	invalid, err := http.Post(server.URL+"/webhooks/demo", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if invalid.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d, want 401", invalid.StatusCode)
	}
	invalid.Body.Close()

	largePayload := bytes.Repeat([]byte("a"), maxPayloadBytes+1)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/webhooks/demo", bytes.NewReader(largePayload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Webhook-Signature", security.Sign(testSecret, largePayload))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized payload status = %d, want 413", response.StatusCode)
	}
}

func sendWebhook(t *testing.T, baseURL string, payload []byte, key string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/webhooks/demo", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-Webhook-Signature", security.Sign(testSecret, payload))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeAndClose(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

type fakeWorker struct {
	running bool
}

func (w *fakeWorker) Notify() {}

func (w *fakeWorker) Running() bool {
	return w.running
}
