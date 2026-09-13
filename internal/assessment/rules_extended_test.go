package assessment

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/application"
	domainassessment "github.com/smartx/sks-migration-center/internal/domain/assessment"
	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

func TestExtendedRulesCoverStorageNetworkSecurityAndImages(t *testing.T) {
	app := application.SourceApplication{ID: uuid.New(), Inventory: application.Inventory{
		Workloads: []application.ResourceSummary{{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api", Requests: map[string]string{"cpu": "1", "memory": "1Gi"}, SecurityRisks: []string{"HOST_PATH", "PRIVILEGED"}}},
		Services:  []application.ResourceSummary{{APIVersion: "v1", Kind: "Service", Namespace: "shop", Name: "api", ServiceType: "LoadBalancer"}},
		Ingresses: []application.ResourceSummary{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Namespace: "shop", Name: "web", IngressClassName: "traefik"}},
		PVCs: []application.VolumeSummary{
			{Name: "database", Namespace: "shop", VolumeMode: "Block", AccessModes: []string{"ReadWriteOnce"}},
			{Name: "shared", Namespace: "shop", VolumeMode: "Filesystem", AccessModes: []string{"ReadWriteMany"}},
		},
		Images: []application.ImageSummary{{Reference: "example/api:latest"}, {Reference: "example/worker:v1"}, {Reference: "example/pinned@sha256:123", Architectures: []string{"amd64"}}},
	}}
	target := environment.Capabilities{Architectures: []string{"amd64"}, IngressClasses: []string{"nginx"}, StorageClasses: []environment.StorageClass{{Name: "smtx-block", Provisioner: "smtx-elf-csi-driver"}}}
	result := DefaultEngine().Assess(Context{Application: app, Target: target})

	expected := map[string]domainassessment.Severity{
		"STORAGE_RAW_BLOCK":             domainassessment.SeverityBlocker,
		"STORAGE_RWX_TARGET":            domainassessment.SeverityBlocker,
		"NETWORK_INGRESS_CLASS":         domainassessment.SeverityWarning,
		"NETWORK_EXPOSED_SERVICE":       domainassessment.SeverityWarning,
		"SECURITY_HOST_ACCESS":          domainassessment.SeverityBlocker,
		"IMAGE_MUTABLE_REFERENCE":       domainassessment.SeverityWarning,
		"IMAGE_ARCHITECTURE_UNVERIFIED": domainassessment.SeverityInfo,
	}
	seen := map[string]bool{}
	for _, issue := range result.Issues {
		if severity, found := expected[issue.RuleID]; found {
			if issue.RuleID == "IMAGE_MUTABLE_REFERENCE" && issue.Severity == domainassessment.SeverityInfo {
				continue
			}
			if issue.Severity != severity {
				t.Fatalf("rule %s severity=%s, want %s", issue.RuleID, issue.Severity, severity)
			}
			seen[issue.RuleID] = true
		}
	}
	for id := range expected {
		if !seen[id] {
			t.Fatalf("rule %s did not produce a finding: %+v", id, result.Issues)
		}
	}
	if result.BlockerCount != 3 {
		t.Fatalf("blocker count=%d, want 3", result.BlockerCount)
	}
}

func TestRWXRuleAcceptsDiscoveredNFSClass(t *testing.T) {
	app := application.SourceApplication{Inventory: application.Inventory{PVCs: []application.VolumeSummary{{Name: "shared", AccessModes: []string{"ReadWriteMany"}}}}}
	target := environment.Capabilities{StorageClasses: []environment.StorageClass{{Name: "shared", Provisioner: "nfs.csi.k8s.io"}}}
	if findings := (RWXStorageRule{}).Evaluate(Context{Application: app, Target: target}); len(findings) != 0 {
		t.Fatalf("unexpected RWX finding: %+v", findings)
	}
}

func TestRawBlockRuleAllowsOnlyVerifiedCSIDataMoverPath(t *testing.T) {
	app := application.SourceApplication{Inventory: application.Inventory{PVCs: []application.VolumeSummary{{Name: "database", StorageClassName: "legacy-block", VolumeMode: "Block"}}}}
	source := environment.Capabilities{
		OperatingSystems:           []string{"linux"},
		StorageClasses:             []environment.StorageClass{{Name: "legacy-block", Provisioner: "csi.legacy.example"}},
		VolumeSnapshotClassDetails: []environment.VolumeSnapshotClass{{Name: "legacy-snap", Driver: "csi.legacy.example"}},
		CSIDataMover:               environment.CSIDataMoverCapabilities{BackupReady: true},
	}
	target := environment.Capabilities{CSIDataMover: environment.CSIDataMoverCapabilities{RestoreReady: true}}
	findings := (RawBlockVolumeRule{}).Evaluate(Context{Application: app, Source: source, Target: target})
	if len(findings) != 1 || findings[0].Severity != domainassessment.SeverityWarning {
		t.Fatalf("expected a non-blocking verified Data Mover finding, got %+v", findings)
	}
	target.CSIDataMover.RestoreReady = false
	findings = (RawBlockVolumeRule{}).Evaluate(Context{Application: app, Source: source, Target: target})
	if len(findings) != 1 || findings[0].Severity != domainassessment.SeverityBlocker {
		t.Fatalf("expected target runtime to block Raw Block migration, got %+v", findings)
	}
}

func TestImageMutableTagDetection(t *testing.T) {
	for reference, expected := range map[string]bool{
		"nginx": true, "nginx:latest": true, "registry:5000/team/api:v1": false, "api@sha256:abc": false,
	} {
		if actual := imageHasMutableTag(reference); actual != expected {
			t.Fatalf("imageHasMutableTag(%q)=%v, want %v", reference, actual, expected)
		}
	}
	if !strings.Contains((ImageReferenceRule{}).Evaluate(Context{Application: application.SourceApplication{Inventory: application.Inventory{Images: []application.ImageSummary{{Reference: "nginx"}}}}})[0].Title, "latest") {
		t.Fatal("untagged image must be treated as implicit latest")
	}
}

func TestComposeUnresolvedVariablesAreVisibleBlocker(t *testing.T) {
	app := application.SourceApplication{Name: "openclaw", SourceType: application.SourceCompose, Inventory: application.Inventory{
		Compose: &application.ComposeInventory{ProjectName: "openclaw", Warnings: []application.ComposeWarning{{
			Code: "COMPOSE_UNRESOLVED_VARIABLES", Message: "发现 12 个未展开的 Compose 环境变量",
		}}},
	}}
	findings := (ComposeUnresolvedVariablesRule{}).Evaluate(Context{Application: app})
	if len(findings) != 1 || findings[0].Severity != domainassessment.SeverityBlocker || findings[0].Resource.Name != "openclaw" {
		t.Fatalf("expected a visible openclaw blocker, got %+v", findings)
	}
	result := DefaultEngine().Assess(Context{Application: app})
	if result.BlockerCount != 1 || len(result.Issues) != 1 || result.Issues[0].RuleID != "COMPOSE_UNRESOLVED_VARIABLES" {
		t.Fatalf("default assessment did not expose unresolved variables: %+v", result)
	}
}
