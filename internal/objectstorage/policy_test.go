package objectstorage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckedInMinIOSourcePolicyAllowsExplicitOneTimeRiskAcceptance(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "build", "minio", "source.lock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	policy, err := LoadSourcePolicy(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.RequireAllowed(); err != nil {
		t.Fatalf("expected accepted official image to be allowed, got %v", err)
	}
	if policy.SecurityGate.Status != "ACCEPTED_RISK" || !policy.RiskAcceptance.Accepted || len(policy.SecurityGate.Blockers) < 2 {
		t.Fatal("checked-in policy does not record the known high-severity findings")
	}
	if policy.OfficialImage.Repository != "docker.io/minio/minio" || !imageDigestPattern.MatchString(policy.OfficialImage.Digest) {
		t.Fatal("checked-in policy does not pin the official image digest")
	}
}

func TestSourcePolicyCannotClaimPassedWhileKeepingBlockers(t *testing.T) {
	policy := SourcePolicy{
		APIVersion: sourceLockAPIVersion, Kind: sourceLockKind,
		Repository: "https://github.com/minio/minio.git", Ref: "release", Commit: "0123456789012345678901234567890123456789",
		License: "AGPL-3.0-only", SourceOfferRequired: true, ReleaseAllowed: true,
		OfficialImage: OfficialImage{Repository: "docker.io/minio/minio", Tag: "release", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		SecurityGate:  SecurityGate{Status: "PASSED", CheckedAt: time.Unix(1, 0), Blockers: []SecurityBlocker{{ID: "CVE-test", Severity: "HIGH", Reason: "unfixed"}}},
	}
	if err := policy.Validate(); err == nil {
		t.Fatal("expected contradictory release gate to fail")
	}
}
