// Package security contains signature, redaction, and SSRF protections.
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const signaturePrefix = "sha256="

// Sign returns the canonical sha256=<hex> HMAC signature.
func Sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature performs strict decoding and constant-time HMAC comparison.
func VerifySignature(secret string, payload []byte, supplied string) bool {
	if !strings.HasPrefix(supplied, signaturePrefix) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(supplied, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hmac.Equal(decoded, mac.Sum(nil))
}

// PayloadHash returns a lowercase SHA-256 payload digest.
func PayloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
