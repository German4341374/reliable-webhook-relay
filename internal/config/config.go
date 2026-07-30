// Package config loads and validates the relay's JSON configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/model"
)

var channelNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Config is the fully resolved runtime configuration.
type Config struct {
	ListenAddress       string
	DatabasePath        string
	PollInterval        time.Duration
	BaseBackoff         time.Duration
	MaxBackoff          time.Duration
	ShutdownTimeout     time.Duration
	AllowPrivateTargets bool
	Channels            map[string]model.Channel
}

type fileConfig struct {
	ListenAddress   string        `json:"listenAddress"`
	DatabasePath    string        `json:"databasePath"`
	PollInterval    string        `json:"pollInterval"`
	BaseBackoff     string        `json:"baseBackoff"`
	MaxBackoff      string        `json:"maxBackoff"`
	ShutdownTimeout string        `json:"shutdownTimeout"`
	Channels        []fileChannel `json:"channels"`
}

type fileChannel struct {
	Name        string `json:"name"`
	TargetURL   string `json:"targetURL"`
	SecretEnv   string `json:"secretEnv"`
	Timeout     string `json:"timeout"`
	MaxAttempts int    `json:"maxAttempts"`
}

// Load reads a strict JSON configuration and resolves channel secrets from the environment.
func Load(path string, getenv func(string) string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var raw fileConfig
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddress:       defaultString(raw.ListenAddress, ":8080"),
		DatabasePath:        defaultString(raw.DatabasePath, "relay.db"),
		AllowPrivateTargets: strings.EqualFold(getenv("RELAY_ALLOW_PRIVATE_TARGETS"), "true"),
		Channels:            make(map[string]model.Channel, len(raw.Channels)),
	}
	if cfg.PollInterval, err = parseDuration(raw.PollInterval, "pollInterval", time.Second); err != nil {
		return Config{}, err
	}
	if cfg.BaseBackoff, err = parseDuration(raw.BaseBackoff, "baseBackoff", time.Second); err != nil {
		return Config{}, err
	}
	if cfg.MaxBackoff, err = parseDuration(raw.MaxBackoff, "maxBackoff", time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = parseDuration(raw.ShutdownTimeout, "shutdownTimeout", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.BaseBackoff > cfg.MaxBackoff {
		return Config{}, errors.New("baseBackoff must not exceed maxBackoff")
	}
	if len(raw.Channels) == 0 {
		return Config{}, errors.New("at least one channel is required")
	}

	for _, rawChannel := range raw.Channels {
		channel, channelErr := resolveChannel(rawChannel, getenv)
		if channelErr != nil {
			return Config{}, channelErr
		}
		if _, exists := cfg.Channels[channel.Name]; exists {
			return Config{}, fmt.Errorf("duplicate channel name %q", channel.Name)
		}
		cfg.Channels[channel.Name] = channel
	}
	return cfg, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing configuration data: %w", err)
	}
	return errors.New("configuration must contain exactly one JSON object")
}

func resolveChannel(raw fileChannel, getenv func(string) string) (model.Channel, error) {
	if !channelNamePattern.MatchString(raw.Name) {
		return model.Channel{}, fmt.Errorf("channel name %q must match %s", raw.Name, channelNamePattern)
	}
	if strings.TrimSpace(raw.TargetURL) == "" {
		return model.Channel{}, fmt.Errorf("channel %q targetURL is required", raw.Name)
	}
	if strings.TrimSpace(raw.SecretEnv) == "" {
		return model.Channel{}, fmt.Errorf("channel %q secretEnv is required", raw.Name)
	}
	secret := getenv(raw.SecretEnv)
	if len(secret) < 16 {
		return model.Channel{}, fmt.Errorf("channel %q secret in %s must contain at least 16 characters", raw.Name, raw.SecretEnv)
	}
	timeout, err := parseDuration(raw.Timeout, fmt.Sprintf("channel %q timeout", raw.Name), 5*time.Second)
	if err != nil {
		return model.Channel{}, err
	}
	if timeout > 2*time.Minute {
		return model.Channel{}, fmt.Errorf("channel %q timeout must not exceed 2m", raw.Name)
	}
	if raw.MaxAttempts < 1 || raw.MaxAttempts > 20 {
		return model.Channel{}, fmt.Errorf("channel %q maxAttempts must be between 1 and 20", raw.Name)
	}
	return model.Channel{
		Name:        raw.Name,
		TargetURL:   raw.TargetURL,
		Secret:      secret,
		Timeout:     timeout,
		MaxAttempts: raw.MaxAttempts,
	}, nil
}

func parseDuration(value, field string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", field)
	}
	return duration, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
