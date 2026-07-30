package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/metrics"
	"github.com/German4341374/reliable-webhook-relay/internal/model"
	"github.com/German4341374/reliable-webhook-relay/internal/security"
	"github.com/German4341374/reliable-webhook-relay/internal/storage"
)

type deliveryStore interface {
	ClaimNext(context.Context, time.Time) (*model.Delivery, error)
	MarkDelivered(context.Context, string, int) error
	MarkRetry(context.Context, string, time.Time, *int, string) error
	MarkDeadLetter(context.Context, string, *int, string) error
	RecoverInFlight(context.Context, time.Time) (int64, error)
}

type Worker struct {
	store        deliveryStore
	channels     map[string]model.Channel
	client       *http.Client
	metrics      *metrics.Counters
	logger       *slog.Logger
	pollInterval time.Duration
	baseBackoff  time.Duration
	maxBackoff   time.Duration
	now          func() time.Time
	jitter       func() float64
	wake         chan struct{}
	running      atomic.Bool
	wg           sync.WaitGroup
}

func New(
	store deliveryStore,
	channels map[string]model.Channel,
	client *http.Client,
	counters *metrics.Counters,
	logger *slog.Logger,
	pollInterval time.Duration,
	baseBackoff time.Duration,
	maxBackoff time.Duration,
) *Worker {
	return &Worker{
		store:        store,
		channels:     channels,
		client:       client,
		metrics:      counters,
		logger:       logger,
		pollInterval: pollInterval,
		baseBackoff:  baseBackoff,
		maxBackoff:   maxBackoff,
		now:          func() time.Time { return time.Now().UTC() },
		jitter:       rand.Float64,
		wake:         make(chan struct{}, 1),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(ctx)
	}()
}

func (w *Worker) Wait() {
	w.wg.Wait()
}

func (w *Worker) Running() bool {
	return w.running.Load()
}

func (w *Worker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func Backoff(base, maximum time.Duration, attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}

	exponent := math.Pow(2, float64(attempt-1))
	raw := float64(base) * exponent
	if raw > float64(maximum) {
		raw = float64(maximum)
	}

	// Add up to 25 percent positive jitter while respecting the configured cap.
	withJitter := raw * (1 + (0.25 * jitter))
	if withJitter > float64(maximum) {
		withJitter = float64(maximum)
	}
	return time.Duration(withJitter)
}

func (w *Worker) run(ctx context.Context) {
	w.running.Store(true)
	defer w.running.Store(false)

	recovered, err := w.store.RecoverInFlight(ctx, w.now())
	if err != nil {
		w.logger.Error("failed to recover in-flight deliveries", "error", err)
	} else if recovered > 0 {
		w.logger.Warn("recovered interrupted deliveries", "count", recovered)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		processed, processErr := w.processOne(ctx)
		if processErr != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Error("delivery worker error", "error", processErr)
		}
		if processed {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
	}
}

func (w *Worker) processOne(ctx context.Context) (bool, error) {
	delivery, err := w.store.ClaimNext(ctx, w.now())
	if err != nil {
		return false, err
	}
	if delivery == nil {
		return false, nil
	}

	channel, ok := w.channels[delivery.Channel]
	if !ok {
		w.metrics.IncFailed()
		return true, w.store.MarkDeadLetter(
			ctx,
			delivery.ID,
			nil,
			"configured channel no longer exists",
		)
	}

	requestContext, cancel := context.WithTimeout(ctx, channel.Timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		channel.TargetURL,
		bytes.NewReader(delivery.Payload),
	)
	if err != nil {
		return true, w.recordFailure(ctx, delivery, channel, nil, err)
	}
	request.Header.Set("Content-Type", delivery.ContentType)
	request.Header.Set("X-Webhook-Signature", security.Sign(channel.Secret, delivery.Payload))
	request.Header.Set("X-Relay-Delivery-ID", delivery.ID)
	if delivery.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", delivery.IdempotencyKey)
	}

	response, requestErr := w.client.Do(request)
	if requestErr != nil {
		return true, w.recordFailure(ctx, delivery, channel, nil, requestErr)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := w.store.MarkDelivered(ctx, delivery.ID, response.StatusCode); err != nil {
			return true, err
		}
		w.metrics.IncDelivered()
		w.logger.Info(
			"delivery succeeded",
			"delivery_id",
			delivery.ID,
			"channel",
			delivery.Channel,
			"attempt",
			delivery.Attempts,
			"response_status",
			response.StatusCode,
		)
		return true, nil
	}

	status := response.StatusCode
	return true, w.recordFailure(
		ctx,
		delivery,
		channel,
		&status,
		fmt.Errorf("target returned HTTP %d", response.StatusCode),
	)
}

func (w *Worker) recordFailure(
	ctx context.Context,
	delivery *model.Delivery,
	channel model.Channel,
	responseStatus *int,
	cause error,
) error {
	message := truncate(safeError(cause), 512)
	now := w.now()
	w.metrics.IncFailed()

	if delivery.Attempts >= channel.MaxAttempts {
		if err := w.store.MarkDeadLetter(
			ctx,
			delivery.ID,
			responseStatus,
			message,
		); err != nil {
			return err
		}
		w.logger.Error(
			"delivery moved to dead letter",
			"delivery_id",
			delivery.ID,
			"channel",
			delivery.Channel,
			"attempts",
			delivery.Attempts,
			"error",
			message,
		)
		return nil
	}

	delay := Backoff(w.baseBackoff, w.maxBackoff, delivery.Attempts, w.jitter())
	nextAttempt := now.Add(delay)
	if err := w.store.MarkRetry(
		ctx,
		delivery.ID,
		nextAttempt,
		responseStatus,
		message,
	); err != nil {
		return err
	}
	w.metrics.IncRetry()
	w.logger.Warn(
		"delivery retry scheduled",
		"delivery_id",
		delivery.ID,
		"channel",
		delivery.Channel,
		"attempt",
		delivery.Attempts,
		"next_attempt_at",
		nextAttempt,
		"error",
		message,
	)
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func safeError(cause error) string {
	var urlError *url.Error
	if errors.As(cause, &urlError) {
		return fmt.Sprintf("%s request failed: %v", urlError.Op, urlError.Err)
	}
	return cause.Error()
}

var _ deliveryStore = (*storage.Store)(nil)
