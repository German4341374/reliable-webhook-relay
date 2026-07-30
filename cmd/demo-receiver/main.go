package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/security"
)

const maxPayloadBytes = 1 << 20

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("demo receiver stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	address := envOrDefault("DEMO_RECEIVER_ADDRESS", ":9090")
	secret := os.Getenv("RELAY_CHANNEL_DEMO_SECRET")
	if len(secret) < 16 {
		return errors.New("RELAY_CHANNEL_DEMO_SECRET must contain at least 16 characters")
	}
	failFirst, err := strconv.Atoi(envOrDefault("DEMO_FAIL_FIRST", "0"))
	if err != nil || failFirst < 0 {
		return errors.New("DEMO_FAIL_FIRST must be a non-negative integer")
	}

	var attempts atomic.Uint64
	var received atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)
		payload, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		if !security.VerifySignature(secret, payload, r.Header.Get("X-Webhook-Signature")) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		attempt := attempts.Add(1)
		hash := sha256.Sum256(payload)
		logger.Info(
			"demo webhook received",
			"attempt",
			attempt,
			"payload_hash",
			hex.EncodeToString(hash[:]),
			"headers",
			security.RedactHeaders(r.Header),
		)
		if attempt <= uint64(failFirst) {
			http.Error(w, "simulated temporary failure", http.StatusServiceUnavailable)
			return
		}
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /received", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]uint64{
			"attempts": attempts.Load(),
			"received": received.Load(),
		})
	})

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("demo receiver listening", "address", address, "fail_first", failFirst)
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case signal := <-signals:
		logger.Info("shutdown signal received", "signal", signal.String())
	case serverErr := <-serverErrors:
		if !errors.Is(serverErr, http.ErrServerClosed) {
			return serverErr
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
