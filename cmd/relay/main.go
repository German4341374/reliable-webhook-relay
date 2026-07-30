package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/api"
	"github.com/German4341374/reliable-webhook-relay/internal/config"
	"github.com/German4341374/reliable-webhook-relay/internal/metrics"
	"github.com/German4341374/reliable-webhook-relay/internal/security"
	"github.com/German4341374/reliable-webhook-relay/internal/storage"
	"github.com/German4341374/reliable-webhook-relay/internal/worker"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config/config.example.json", "path to JSON configuration")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(*configPath, logger); err != nil {
		logger.Error("relay stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath, os.Getenv)
	if err != nil {
		return err
	}
	if cfg.AllowPrivateTargets {
		logger.Warn("private target protection is disabled for local development")
	}

	validationContext, cancelValidation := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelValidation()
	for name, channel := range cfg.Channels {
		if err := security.ValidateTarget(
			validationContext,
			channel.TargetURL,
			cfg.AllowPrivateTargets,
			net.DefaultResolver,
		); err != nil {
			return fmt.Errorf("validate channel %q target: %w", name, err)
		}
		logger.Info(
			"validated channel target",
			"channel",
			name,
			"target",
			security.SafeURLForLog(channel.TargetURL),
		)
	}

	rootContext, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := storage.Open(rootContext, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	counters := &metrics.Counters{}
	deliveryWorker := worker.New(
		store,
		cfg.Channels,
		security.NewHTTPClient(cfg.AllowPrivateTargets, logger),
		counters,
		logger,
		cfg.PollInterval,
		cfg.BaseBackoff,
		cfg.MaxBackoff,
	)
	deliveryWorker.Start(rootContext)

	handler := api.New(store, cfg.Channels, deliveryWorker, counters, logger).Handler()
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("relay listening", "address", cfg.ListenAddress, "version", version)
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
			cancel()
			deliveryWorker.Wait()
			return fmt.Errorf("serve HTTP: %w", serverErr)
		}
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("HTTP shutdown did not finish cleanly", "error", err)
	}
	cancel()
	deliveryWorker.Wait()
	logger.Info("relay shutdown complete")
	return nil
}
