package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/metrics"
	"github.com/German4341374/reliable-webhook-relay/internal/model"
	"github.com/German4341374/reliable-webhook-relay/internal/security"
	"github.com/German4341374/reliable-webhook-relay/internal/storage"
)

const maxPayloadBytes = 1 << 20

type deliveryStore interface {
	CreateDelivery(context.Context, model.Delivery) (model.Delivery, bool, error)
	GetDelivery(context.Context, string) (model.Delivery, error)
	ListDeliveries(context.Context, int, string, string) ([]model.Delivery, error)
	RetryDeadLetter(context.Context, string, time.Time) error
	Ping(context.Context) error
}

type workerControl interface {
	Notify()
	Running() bool
}

type API struct {
	store    deliveryStore
	channels map[string]model.Channel
	worker   workerControl
	metrics  *metrics.Counters
	logger   *slog.Logger
	now      func() time.Time
}

func New(
	store deliveryStore,
	channels map[string]model.Channel,
	worker workerControl,
	counters *metrics.Counters,
	logger *slog.Logger,
) *API {
	return &API{
		store:    store,
		channels: channels,
		worker:   worker,
		metrics:  counters,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/{channel}", a.receiveWebhook)
	mux.HandleFunc("GET /deliveries", a.listDeliveries)
	mux.HandleFunc("GET /deliveries/{id}", a.getDelivery)
	mux.HandleFunc("POST /deliveries/{id}/retry", a.retryDelivery)
	mux.HandleFunc("GET /health", a.health)
	mux.Handle("GET /metrics", a.metrics)
	return a.recoverPanic(a.logRequests(mux))
}

func (a *API) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	channel, ok := a.channels[r.PathValue("channel")]
	if !ok {
		writeError(w, http.StatusNotFound, "channel_not_found", "webhook channel was not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "payload must not exceed 1 MiB")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_payload", "request body could not be read")
		return
	}
	if !security.VerifySignature(channel.Secret, payload, r.Header.Get("X-Webhook-Signature")) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "webhook signature is missing or invalid")
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 200 || containsControlCharacter(idempotencyKey) {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must be at most 200 safe characters")
		return
	}

	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if len(contentType) > 200 || containsControlCharacter(contentType) {
		writeError(w, http.StatusBadRequest, "invalid_content_type", "Content-Type is invalid")
		return
	}

	now := a.now()
	delivery := model.Delivery{
		ID:             newID(),
		Channel:        channel.Name,
		ReceivedAt:     now,
		Status:         model.StatusPending,
		NextAttemptAt:  &now,
		PayloadHash:    security.PayloadHash(payload),
		Payload:        payload,
		ContentType:    contentType,
		IdempotencyKey: idempotencyKey,
	}
	persisted, created, err := a.store.CreateDelivery(r.Context(), delivery)
	if err != nil {
		a.logger.Error("failed to persist delivery", "channel", channel.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "delivery could not be persisted")
		return
	}
	if !created && persisted.PayloadHash != delivery.PayloadHash {
		writeError(
			w,
			http.StatusConflict,
			"idempotency_conflict",
			"Idempotency-Key was already used with a different payload",
		)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusAccepted
		a.metrics.IncReceived()
		a.worker.Notify()
	}
	writeJSON(w, status, map[string]any{
		"delivery":  persisted,
		"duplicate": !created,
	})
}

func (a *API) listDeliveries(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid_status", "status filter is invalid")
		return
	}
	channel := r.URL.Query().Get("channel")

	deliveries, err := a.store.ListDeliveries(r.Context(), limit, status, channel)
	if err != nil {
		a.logger.Error("failed to list deliveries", "error", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "deliveries could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deliveries": deliveries,
		"count":      len(deliveries),
	})
}

func (a *API) getDelivery(w http.ResponseWriter, r *http.Request) {
	delivery, err := a.store.GetDelivery(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "delivery_not_found", "delivery was not found")
		return
	}
	if err != nil {
		a.logger.Error("failed to load delivery", "error", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "delivery could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, delivery)
}

func (a *API) retryDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.store.RetryDeadLetter(r.Context(), id, a.now())
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "delivery_not_found", "delivery was not found")
		return
	case errors.Is(err, storage.ErrNotRetryable):
		writeError(w, http.StatusConflict, "delivery_not_retryable", "only dead-letter deliveries can be retried")
		return
	case err != nil:
		a.logger.Error("failed to retry delivery", "delivery_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "storage_error", "delivery could not be retried")
		return
	}
	a.worker.Notify()
	delivery, err := a.store.GetDelivery(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "delivery was reset but could not be loaded")
		return
	}
	writeJSON(w, http.StatusAccepted, delivery)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "unhealthy",
			"database": "unavailable",
			"worker":   workerState(a.worker.Running()),
		})
		return
	}
	if !a.worker.Running() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":   "unhealthy",
			"database": "ok",
			"worker":   "stopped",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"database": "ok",
		"worker":   "running",
	})
}

func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		a.logger.Info(
			"http request",
			"method",
			r.Method,
			"path",
			r.URL.Path,
			"status",
			recorder.status,
			"duration_ms",
			time.Since(started).Milliseconds(),
			"headers",
			security.RedactHeaders(r.Header),
		)
	})
}

func (a *API) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				a.logger.Error("recovered HTTP panic", "path", r.URL.Path, "panic", recovered)
				writeError(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("secure random source unavailable")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32]
}

func validStatus(value string) bool {
	switch model.DeliveryStatus(value) {
	case model.StatusPending,
		model.StatusProcessing,
		model.StatusRetrying,
		model.StatusDelivered,
		model.StatusDeadLetter:
		return true
	default:
		return false
	}
}

func containsControlCharacter(value string) bool {
	for _, character := range value {
		if character < 32 || character == 127 {
			return true
		}
	}
	return false
}

func workerState(running bool) string {
	if running {
		return "running"
	}
	return "stopped"
}

var _ deliveryStore = (*storage.Store)(nil)
