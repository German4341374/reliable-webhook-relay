package security

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Resolver is the DNS subset used by SSRF validation.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// ValidateTarget resolves and rejects unsafe URL targets.
func ValidateTarget(ctx context.Context, rawURL string, allowPrivate bool, resolver Resolver) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse target URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported target URL scheme %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return errors.New("target URL hostname is required")
	}
	if parsed.User != nil {
		return errors.New("target URL user information is forbidden")
	}
	addresses, err := resolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return fmt.Errorf("resolve target hostname: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("target hostname resolved to no addresses")
	}
	if allowPrivate {
		return nil
	}
	for _, address := range addresses {
		if unsafeIP(address.IP) {
			return fmt.Errorf("target hostname resolves to forbidden address %s", address.IP)
		}
	}
	return nil
}

func unsafeIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// NewHTTPClient creates a client whose dialer rechecks DNS results before every connection.
func NewHTTPClient(allowPrivate bool, logger *slog.Logger) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split target address: %w", err)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve target hostname: %w", err)
		}
		for _, candidate := range addresses {
			if !allowPrivate && unsafeIP(candidate.IP) {
				logger.Warn("blocked unsafe delivery address", "host", host, "ip", candidate.IP.String())
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
		return nil, errors.New("target resolved only to forbidden addresses")
	}

	client := &http.Client{Transport: transport}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		// Preserve POST semantics and force operators to configure the final target.
		return http.ErrUseLastResponse
	}
	return client
}

// SafeURLForLog removes query values and fragments from a URL.
func SafeURLForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "[invalid-url]"
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted"
	}
	parsed.Fragment = ""
	parsed.User = nil
	return strings.TrimSpace(parsed.String())
}
