package security

import (
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, "correct horse") || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected encoded hash %q", hash)
	}
	valid, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !valid {
		t.Fatalf("VerifyPassword(valid) = %v, %v", valid, err)
	}
	valid, err = VerifyPassword("incorrect password", hash)
	if err != nil || valid {
		t.Fatalf("VerifyPassword(invalid) = %v, %v", valid, err)
	}
}

func TestPasswordHashRejectsShortSecret(t *testing.T) {
	if _, err := HashPassword("too-short"); err == nil {
		t.Fatal("expected short password to be rejected")
	}
}
