package preflight

import "context"

// Checker evaluates one readiness concern against a Snapshot and records
// results through Builder.
type Checker interface {
	Name() string
	Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder)
}
