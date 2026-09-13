package s3

import (
	"strings"
	"testing"
)

func TestNewClientEnforcesVerifiedEndpointBoundary(t *testing.T) {
	valid := Config{Endpoint: "https://minio.example.test", AccessKey: "access", SecretKey: "secret", TLSVerify: true}
	if _, err := newClient(valid); err != nil {
		t.Fatalf("verified HTTPS endpoint was rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"HTTP with verification": func(value *Config) { value.Endpoint = "http://minio.example.test" },
		"path":                   func(value *Config) { value.Endpoint = "https://minio.example.test/s3" },
		"userinfo":               func(value *Config) { value.Endpoint = "https://user@minio.example.test" },
		"missing secret":         func(value *Config) { value.SecretKey = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if _, err := newClient(value); err == nil {
				t.Fatal("expected unsafe S3 configuration to be rejected")
			}
		})
	}
}

func TestNewClientRejectsInvalidCABundle(t *testing.T) {
	_, err := newClient(Config{
		Endpoint: "https://minio.example.test", AccessKey: "access", SecretKey: "secret",
		TLSVerify: true, CABundle: []byte("not a certificate"),
	})
	if err == nil || !strings.Contains(err.Error(), "CA bundle") {
		t.Fatalf("expected invalid CA bundle error, got %v", err)
	}
}
