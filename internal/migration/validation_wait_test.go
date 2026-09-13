package migration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
)

func TestWaitForNamespaceValidationAllowsWorkloadReadinessWindow(t *testing.T) {
	attempts := 0
	result, err := waitForNamespaceValidation(context.Background(), time.Second, time.Millisecond, func(context.Context) (kubernetesadapter.NamespaceValidation, error) {
		attempts++
		if attempts < 3 {
			return kubernetesadapter.NamespaceValidation{Deployments: 1}, errors.New("Deployment guestbook/frontend is not ready: desired=1 available=0")
		}
		return kubernetesadapter.NamespaceValidation{Deployments: 1}, nil
	})
	if err != nil || attempts != 3 || result.Deployments != 1 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}
}

func TestWaitForNamespaceValidationReturnsLastEvidenceOnTimeout(t *testing.T) {
	result, err := waitForNamespaceValidation(context.Background(), 4*time.Millisecond, time.Millisecond, func(context.Context) (kubernetesadapter.NamespaceValidation, error) {
		return kubernetesadapter.NamespaceValidation{Deployments: 1}, errors.New("desired=1 available=0")
	})
	if err == nil || !strings.Contains(err.Error(), "did not become ready") || result.Deployments != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
