package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// NOTAMChecker records NOTAM/restriction readiness from a NOTAMProvider.
//
// Limitation: the current PreflightStatus / ComplianceFinding model has no
// UNKNOWN value. Provider errors are therefore recorded as blocking failures
// rather than silent clears; a future status-model PR can map them to UNKNOWN.
type NOTAMChecker struct {
	provider NOTAMProvider
}

func (NOTAMChecker) Name() string { return "notam" }

func (c NOTAMChecker) Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder) {
	result, err := c.provider.Check(ctx, snapshot)
	if err != nil {
		builder.Block(
			domain.PreflightCheckNOTAM,
			"demo_notam",
			"notam_provider",
			"NOTAM-PROVIDER",
			"notam provider failed",
			"retry notam lookup; provider failure is not treated as clear",
		)
		return
	}
	if result.Clear {
		builder.Clear(domain.PreflightCheckNOTAM, result.Key, result.Source, result.RequirementCode, result.Summary)
		return
	}
	builder.Block(domain.PreflightCheckNOTAM, result.Key, result.Source, result.RequirementCode, result.Summary, result.Remediation)
}
