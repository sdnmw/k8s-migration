package kubernetes

import (
	"strings"
	"testing"
)

func TestEndpointValidationJobUsesInClusterHTTPAndTCPChecks(t *testing.T) {
	spec := EndpointValidationSpec{Namespace: "shop", RunID: "run-1", HTTPChecks: []string{"http://api.shop.svc/healthz"}, TCPChecks: []string{"redis.shop.svc:6379"}, HelperImage: "busybox@sha256:test"}
	if err := validateEndpointValidationSpec(spec); err != nil {
		t.Fatal(err)
	}
	job := endpointValidationJob(spec, endpointValidationJobName(spec.RunID))
	command := job.Spec.Template.Spec.Containers[0].Args[0]
	if !strings.Contains(command, "wget -q --spider") || !strings.Contains(command, "nc -z -w 15") {
		t.Fatalf("missing endpoint checks: %s", command)
	}
	if job.Spec.Template.Spec.AutomountServiceAccountToken == nil || *job.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatal("validation Job must not mount a service account token")
	}
}

func TestEndpointValidationRejectsMalformedTargets(t *testing.T) {
	base := EndpointValidationSpec{Namespace: "shop", RunID: "run", HelperImage: "busybox@sha256:test"}
	base.HTTPChecks = []string{"file:///etc/passwd"}
	if err := validateEndpointValidationSpec(base); err == nil {
		t.Fatal("expected invalid HTTP target")
	}
	base.HTTPChecks, base.TCPChecks = nil, []string{"redis:bad"}
	if err := validateEndpointValidationSpec(base); err == nil {
		t.Fatal("expected invalid TCP target")
	}
}
