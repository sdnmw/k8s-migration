package security

import (
	"bytes"
	"testing"
)

func TestEnvelopeEncryptionRoundTripAndRandomness(t *testing.T) {
	keyring, err := NewKeyring(1, map[int][]byte{1: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	plaintext := []byte(`{"token":"secret-value"}`)
	aad := []byte("credential-id:KUBECONFIG")
	first, err := keyring.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := keyring.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt second: %v", err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("secret-value")) {
		t.Fatal("envelope encryption must be randomized and hide plaintext")
	}
	decrypted, err := keyring.Decrypt(first, aad)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypt = %q, %v", decrypted, err)
	}
	if _, err := keyring.Decrypt(first, []byte("different-context")); err == nil {
		t.Fatal("expected associated-data mismatch to fail")
	}
}

func TestEnvelopeRejectsTampering(t *testing.T) {
	keyring, _ := NewKeyring(1, map[int][]byte{1: bytes.Repeat([]byte{9}, 32)})
	encoded, _ := keyring.Encrypt([]byte("private-key"), []byte("ssh"))
	encoded[len(encoded)-3] ^= 1
	if _, err := keyring.Decrypt(encoded, []byte("ssh")); err == nil {
		t.Fatal("expected tampered envelope to fail")
	}
}
