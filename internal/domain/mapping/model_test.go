package mapping

import (
	"strings"
	"testing"

	"github.com/smartx/sks-migration-center/internal/domain/environment"
)

func TestNormalizeAndValidateSortsAllMappings(t *testing.T) {
	profile := Profile{Name: " production ",
		Storage:    []KeyValue{{Source: "slow", Target: "smtx-block"}, {Source: "fast", Target: "smtx-block"}},
		Namespaces: []KeyValue{{Source: "shop", Target: "shop-prod"}},
		Ingress:    []KeyValue{{Source: "traefik", Target: "nginx"}},
		Registries: []KeyValue{{Source: "z.example", Target: "harbor.example/z"}, {Source: "a.example", Target: "harbor.example/a"}},
		NFS:        []NFSMapping{{SourceServer: "10.0.0.2", SourceExport: "/data", TargetServer: "10.0.1.2", TargetExport: "/migrations", TargetStorageClass: "nfs-rwx"}},
		NodeLabels: []NodeLabelMapping{{Source: "legacy/rack", Action: "drop", Target: "ignored"}, {Source: "zone", Target: "topology.kubernetes.io/zone", Action: "map"}},
	}
	target := environment.Capabilities{StorageClasses: []environment.StorageClass{{Name: "smtx-block"}, {Name: "nfs-rwx"}}, IngressClasses: []string{"nginx"}}
	if err := profile.NormalizeAndValidate(target); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if profile.Name != "production" || profile.Storage[0].Source != "fast" || profile.Registries[0].Source != "a.example" || profile.NodeLabels[0].Action != "DROP" || profile.NodeLabels[0].Target != "" {
		t.Fatalf("profile was not normalized: %+v", profile)
	}
}

func TestNormalizeAndValidateRejectsConflicts(t *testing.T) {
	target := environment.Capabilities{StorageClasses: []environment.StorageClass{{Name: "smtx-block"}}, IngressClasses: []string{"nginx"}}
	tests := map[string]Profile{
		"namespace collision":  {Name: "x", Namespaces: []KeyValue{{Source: "one", Target: "same"}, {Source: "two", Target: "same"}}},
		"duplicate source":     {Name: "x", Registries: []KeyValue{{Source: "registry", Target: "one"}, {Source: "registry", Target: "two"}}},
		"overlapping registry": {Name: "x", Registries: []KeyValue{{Source: "registry/team", Target: "one"}, {Source: "registry/team/api", Target: "two"}}},
		"unknown storage":      {Name: "x", Storage: []KeyValue{{Source: "old", Target: "missing"}}},
		"unknown ingress":      {Name: "x", Ingress: []KeyValue{{Source: "old", Target: "missing"}}},
		"invalid NFS export":   {Name: "x", NFS: []NFSMapping{{SourceServer: "source", SourceExport: "relative", TargetServer: "target", TargetExport: "/data", TargetStorageClass: "smtx-block"}}},
		"invalid label action": {Name: "x", NodeLabels: []NodeLabelMapping{{Source: "rack", Target: "zone", Action: "COPY"}}},
	}
	for name, profile := range tests {
		t.Run(name, func(t *testing.T) {
			if err := profile.NormalizeAndValidate(target); err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("expected deterministic validation error, got %v", err)
			}
		})
	}
}
