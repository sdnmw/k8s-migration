package migration

import "testing"

func TestValidateTransition(t *testing.T) {
	if err := ValidateTransition(RunFinalBackup, RunTransform); err != nil {
		t.Fatalf("resource-only plan must advance without a transfer step: %v", err)
	}
	if err := ValidateTransition(RunPending, RunPreflight); err != nil {
		t.Fatalf("expected transition to be valid: %v", err)
	}
	if err := ValidateTransition(RunPending, RunCompleted); err == nil {
		t.Fatal("expected transition to be rejected")
	}
	if err := ValidateTransition(RunAwaitingCutover, RunRollingBack); err != nil {
		t.Fatalf("expected rollback before cutover to be valid: %v", err)
	}
	if err := ValidateTransition(RunCompleted, RunRollingBack); err != nil {
		t.Fatalf("completed migration must support operator-requested source restoration: %v", err)
	}
}

func TestEveryNonTerminalRunCanFailOrRollback(t *testing.T) {
	for status, transitions := range allowedTransitions {
		if status == RunPending {
			continue
		}
		_, canFail := transitions[RunFailed]
		_, canRollback := transitions[RunRollingBack]
		if !canFail && !canRollback {
			t.Errorf("status %s has no failure or rollback transition", status)
		}
	}
}

func TestQuiescedSourceMustRollbackBeforeFailureOrCancellation(t *testing.T) {
	for _, status := range []RunStatus{
		RunQuiesce,
		RunFinalBackup,
		RunTransfer,
		RunTransform,
		RunRestore,
		RunValidation,
		RunAwaitingCutover,
	} {
		if err := ValidateTransition(status, RunRollingBack); err != nil {
			t.Errorf("%s should allow rollback: %v", status, err)
		}
		if err := ValidateTransition(status, RunFailed); err == nil {
			t.Errorf("%s must not fail without rollback", status)
		}
		if err := ValidateTransition(status, RunCancelled); err == nil {
			t.Errorf("%s must not cancel without rollback", status)
		}
	}
}
