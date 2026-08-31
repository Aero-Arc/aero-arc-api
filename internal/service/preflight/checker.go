package preflight

import "context"

// Checker evaluates one readiness concern against a Snapshot and records
// results through Builder. Existing policy stays in service.go until PR 3.
type Checker interface {
	Name() string
	Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder)
}
