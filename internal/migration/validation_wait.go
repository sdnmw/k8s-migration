package migration

import (
	"context"
	"fmt"
	"time"

	kubernetesadapter "github.com/smartx/sks-migration-center/internal/adapter/kubernetes"
)

const defaultValidationTimeout = 5 * time.Minute

// waitForNamespaceValidation keeps the validation step inside the plan's
// readiness window. A restored workload can exist before its image is pulled
// and readiness probes pass; treating that first observation as final creates
// false migration failures.
func waitForNamespaceValidation(
	ctx context.Context,
	timeout time.Duration,
	pollInterval time.Duration,
	validate func(context.Context) (kubernetesadapter.NamespaceValidation, error),
) (kubernetesadapter.NamespaceValidation, error) {
	if timeout <= 0 {
		timeout = defaultValidationTimeout
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, lastErr := validate(waitCtx)
	if lastErr == nil {
		return result, nil
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, fmt.Errorf("target resources did not become ready within %s: %w", timeout, lastErr)
		case <-ticker.C:
			result, lastErr = validate(waitCtx)
			if lastErr == nil {
				return result, nil
			}
		}
	}
}
