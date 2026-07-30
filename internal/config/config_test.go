package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesSecretAndDefaults(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `{
	  "channels": [{
	    "name": "alerts",
	    "targetURL": "https://hooks.example.test/events",
	    "secretEnv": "ALERT_SECRET",
	    "maxAttempts": 4
	  }]
	}`)
	cfg, err := Load(path, func(name string) string {
		if name == "ALERT_SECRET" {
			return "test-secret-at-least-16-characters"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != ":8080" || cfg.Channels["alerts"].MaxAttempts != 4 {
		t.Fatalf("unexpected configuration: %+v", cfg)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `{"unexpected":true,"channels":[]}`)
	_, err := Load(path, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRejectsShortSecret(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `{
	  "channels": [{
	    "name": "alerts",
	    "targetURL": "https://hooks.example.test/events",
	    "secretEnv": "ALERT_SECRET",
	    "maxAttempts": 4
	  }]
	}`)
	_, err := Load(path, func(string) string { return "short" })
	if err == nil || !strings.Contains(err.Error(), "at least 16") {
		t.Fatalf("expected short secret error, got %v", err)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
