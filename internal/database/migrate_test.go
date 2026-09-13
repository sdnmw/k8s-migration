package database

import (
	"strings"
	"testing"
)

func TestEmbeddedMigrations(t *testing.T) {
	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations returned an error: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	if migrations[0].Version != "000001_initial.sql" {
		t.Fatalf("unexpected first migration: %s", migrations[0].Version)
	}
	for _, table := range []string{"environments", "migration_plans", "migration_runs", "job_leases", "audit_events"} {
		if !strings.Contains(migrations[0].SQL, "CREATE TABLE "+table) {
			t.Fatalf("initial migration does not create %s", table)
		}
	}
	if len(migrations[0].Checksum) != 64 {
		t.Fatalf("expected SHA-256 checksum, got %q", migrations[0].Checksum)
	}
}
