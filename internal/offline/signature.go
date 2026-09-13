package offline

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
)

type ArchiveSignature struct {
	SchemaVersion int    `json:"schemaVersion"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"keyId"`
	BundleSHA256  string `json:"bundleSha256"`
	PublicKey     string `json:"publicKey"`
	Signature     string `json:"signature"`
}

func SignArchive(path string, privateKey io.Reader) (ArchiveSignature, error) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return ArchiveSignature{}, err
	}
	digest, err := archiveDigest(path)
	if err != nil {
		return ArchiveSignature{}, err
	}
	publicKey := key.Public().(ed25519.PublicKey)
	keyDigest := sha256.Sum256(publicKey)
	return ArchiveSignature{
		SchemaVersion: 1, Algorithm: "Ed25519", KeyID: hex.EncodeToString(keyDigest[:]),
		BundleSHA256: hex.EncodeToString(digest), PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, digest)),
	}, nil
}

func VerifyArchiveSignature(path string, signature io.Reader, trustedPublicKey io.Reader) error {
	var value ArchiveSignature
	decoder := json.NewDecoder(io.LimitReader(signature, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return errors.New("archive signature bundle is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("archive signature bundle has trailing content")
	}
	if value.SchemaVersion != 1 || value.Algorithm != "Ed25519" {
		return errors.New("archive signature algorithm or schema is unsupported")
	}
	publicKey, err := parsePublicKey(trustedPublicKey)
	if err != nil {
		return err
	}
	embedded, err := base64.StdEncoding.DecodeString(value.PublicKey)
	if err != nil || !publicKey.Equal(ed25519.PublicKey(embedded)) {
		return errors.New("archive signature was not made by the trusted public key")
	}
	keyDigest := sha256.Sum256(publicKey)
	if value.KeyID != hex.EncodeToString(keyDigest[:]) {
		return errors.New("archive signature key ID does not match the trusted public key")
	}
	digest, err := archiveDigest(path)
	if err != nil {
		return err
	}
	if value.BundleSHA256 != hex.EncodeToString(digest) {
		return errors.New("archive digest does not match the signature bundle")
	}
	encodedSignature, err := base64.StdEncoding.DecodeString(value.Signature)
	if err != nil || !ed25519.Verify(publicKey, digest, encodedSignature) {
		return errors.New("archive Ed25519 signature verification failed")
	}
	return nil
}

func EncodeArchiveSignature(output io.Writer, value ArchiveSignature) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func archiveDigest(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open offline archive: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("hash offline archive: %w", err)
	}
	return hash.Sum(nil), nil
}

func parsePrivateKey(input io.Reader) (ed25519.PrivateKey, error) {
	value, err := io.ReadAll(io.LimitReader(input, 64*1024))
	if err != nil {
		return nil, errors.New("read Ed25519 private key")
	}
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, errors.New("Ed25519 private key must be PKCS#8 PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(ed25519.PrivateKey)
	if err != nil || !ok {
		return nil, errors.New("private key is not Ed25519 PKCS#8")
	}
	return key, nil
}

func parsePublicKey(input io.Reader) (ed25519.PublicKey, error) {
	value, err := io.ReadAll(io.LimitReader(input, 64*1024))
	if err != nil {
		return nil, errors.New("read trusted Ed25519 public key")
	}
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, errors.New("trusted Ed25519 public key must be PKIX PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	key, ok := parsed.(ed25519.PublicKey)
	if err != nil || !ok {
		return nil, errors.New("trusted public key is not Ed25519 PKIX")
	}
	return key, nil
}
