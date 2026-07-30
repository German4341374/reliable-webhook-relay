package security

import (
	"context"
	"net"
	"testing"
)

type staticResolver []net.IPAddr

func (r staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r, nil
}

func TestValidateTargetRejectsUnsafeAddresses(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"127.0.0.1", "10.0.0.8", "169.254.1.1", "::1"} {
		resolver := staticResolver{{IP: net.ParseIP(address)}}
		if err := ValidateTarget(context.Background(), "https://example.test/hook", false, resolver); err == nil {
			t.Fatalf("expected %s to be rejected", address)
		}
	}
}

func TestValidateTargetAcceptsPublicHTTPS(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{{IP: net.ParseIP("203.0.113.10")}}
	if err := ValidateTarget(context.Background(), "https://example.test/hook", false, resolver); err != nil {
		t.Fatalf("expected public target to pass: %v", err)
	}
}

func TestValidateTargetRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{{IP: net.ParseIP("203.0.113.10")}}
	if err := ValidateTarget(context.Background(), "file:///tmp/payload", false, resolver); err == nil {
		t.Fatal("expected unsupported scheme to be rejected")
	}
}
