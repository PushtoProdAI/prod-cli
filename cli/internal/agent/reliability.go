package agent

// reliability.go holds the cross-platform reliability decisions shared by the deploy
// workflows, kept as pure functions so the policy is testable without the workflow engine.

// shouldAutoRollback reports whether a deploy that just FAILED its liveness check should
// attempt an automatic rollback. We roll back only when (a) the platform actually
// implements rollback and (b) this was an update — a first-ever deploy has no previous
// version to return to, so there's nothing to roll back and we report failed instead.
// (ACD.2: rollback-capable update → previous serving; else failed + remediation.)
func shouldAutoRollback(supportsRollback, isUpdate bool) bool {
	return supportsRollback && isUpdate
}
