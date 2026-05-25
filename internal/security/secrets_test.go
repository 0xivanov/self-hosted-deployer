package security

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSecretCipherRoundTripUsesUniqueNonces(t *testing.T) {
	cipher, err := NewSecretCipher([]byte(strings.Repeat("a", SecretKeyBytes)))
	if err != nil {
		t.Fatalf("new secret cipher: %v", err)
	}

	first, err := cipher.Encrypt("super-secret")
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	second, err := cipher.Encrypt("super-secret")
	if err != nil {
		t.Fatalf("encrypt secret again: %v", err)
	}
	if first == second {
		t.Fatal("expected randomized ciphertext")
	}
	plaintext, err := cipher.Decrypt(first)
	if err != nil || plaintext != "super-secret" {
		t.Fatalf("decrypt secret got %q: %v", plaintext, err)
	}
}

func TestSecretCipherRejectsTampering(t *testing.T) {
	cipher, err := NewSecretCipher([]byte(strings.Repeat("a", SecretKeyBytes)))
	if err != nil {
		t.Fatalf("new secret cipher: %v", err)
	}
	encoded, err := cipher.Encrypt("super-secret")
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}
	tampered, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	tampered[len(tampered)-1] ^= 1
	if _, err := cipher.Decrypt(base64.RawURLEncoding.EncodeToString(tampered)); err == nil {
		t.Fatal("expected tampered ciphertext to fail")
	}
}
