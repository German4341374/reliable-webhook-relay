package security

import "testing"

func TestSignAndVerifySignature(t *testing.T) {
	t.Parallel()
	secret := "test-secret-with-32-characters"
	payload := []byte(`{"event":"created"}`)

	signature := Sign(secret, payload)
	if !VerifySignature(secret, payload, signature) {
		t.Fatal("expected valid signature to verify")
	}
}

func TestVerifySignatureRejectsModifiedPayload(t *testing.T) {
	t.Parallel()
	secret := "test-secret-with-32-characters"
	signature := Sign(secret, []byte("original"))

	if VerifySignature(secret, []byte("modified"), signature) {
		t.Fatal("modified payload must not verify")
	}
}

func TestVerifySignatureRejectsMalformedValue(t *testing.T) {
	t.Parallel()
	for _, signature := range []string{"", "deadbeef", "sha256=not-hex", "sha1=abcd"} {
		if VerifySignature("test-secret", []byte("payload"), signature) {
			t.Fatalf("malformed signature %q must not verify", signature)
		}
	}
}

func TestPayloadHashIsStable(t *testing.T) {
	t.Parallel()
	const expected = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if actual := PayloadHash([]byte("abc")); actual != expected {
		t.Fatalf("PayloadHash() = %q, want %q", actual, expected)
	}
}
