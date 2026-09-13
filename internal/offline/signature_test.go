package offline

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveSignatureRejectsTamperingAndUntrustedKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, _ := x509.MarshalPKCS8PrivateKey(privateKey)
	publicDER, _ := x509.MarshalPKIXPublicKey(publicKey)
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	archive := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := os.WriteFile(archive, []byte("deterministic archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	signature, err := SignArchive(archive, bytes.NewReader(privatePEM))
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := EncodeArchiveSignature(&encoded, signature); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArchiveSignature(archive, bytes.NewReader(encoded.Bytes()), bytes.NewReader(publicPEM)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("tampered archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArchiveSignature(archive, bytes.NewReader(encoded.Bytes()), bytes.NewReader(publicPEM)); err == nil {
		t.Fatal("expected archive tampering to fail verification")
	}
	otherPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	otherDER, _ := x509.MarshalPKIXPublicKey(otherPublic)
	otherPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: otherDER})
	if err := VerifyArchiveSignature(archive, bytes.NewReader(encoded.Bytes()), bytes.NewReader(otherPEM)); err == nil {
		t.Fatal("expected untrusted key to fail verification")
	}
}
