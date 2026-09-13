package kubernetes

import (
	"strings"
	"testing"
)

func TestKomposeResourcesAreIsolatedAndDoNotExposeCredentials(t *testing.T) {
	spec := KomposeJobSpec{
		SystemNamespace: "sks-migration-system", TargetNamespace: "shop", RunID: "73963a25-2516-4e26-9050-bb7391fe109b",
		ComposeYAML: []byte("services:\n  api:\n    image: nginx\n"), EnvironmentFile: []byte("PASSWORD=secret\n"),
		KomposeImage: "harbor.local/migration/kompose@sha256:test", HelperImage: "harbor.local/migration/busybox@sha256:test",
	}
	secret, job := komposeResources(spec, komposeJobName(spec.RunID))
	if string(secret.Data[".env"]) != "PASSWORD=secret\n" || secret.StringData != nil {
		t.Fatalf("unexpected secret payload: %#v", secret.Data)
	}
	pod := job.Spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken || len(pod.InitContainers) != 1 || len(pod.Containers) != 1 {
		t.Fatalf("job isolation is incomplete: %#v", pod)
	}
	converter := pod.InitContainers[0]
	if converter.SecurityContext == nil || converter.SecurityContext.ReadOnlyRootFilesystem == nil || !*converter.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("converter root filesystem must be read-only")
	}
	command := strings.Join(converter.Args, " ")
	if strings.Contains(command, "secret") || !strings.Contains(command, "--stdout") || strings.Contains(command, "--namespace") {
		t.Fatalf("unexpected converter command: %s", command)
	}
}

func TestValidateKomposeSpecRequiresDigestPinnedImages(t *testing.T) {
	err := validateKomposeSpec(KomposeJobSpec{SystemNamespace: "system", TargetNamespace: "shop", RunID: "run", ComposeYAML: []byte("services: {}"), KomposeImage: "kompose:latest", HelperImage: "busybox@sha256:test"})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("expected digest validation error, got %v", err)
	}
}

func TestKomposeJobNameIsStableAndBounded(t *testing.T) {
	name := komposeJobName("73963a25-2516-4e26-9050-bb7391fe109b")
	if name != "smc-kompose-73963a2525164e269050" || len(name) > 63 {
		t.Fatalf("unexpected job name %q", name)
	}
}
