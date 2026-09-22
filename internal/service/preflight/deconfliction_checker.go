package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

const (
	deconflictionCheckKey    = "deconfliction_current"
	deconflictionSource      = "deconfliction_service"
	deconflictionRequirement = "DECONFLICT-CURRENT"
)

// DeconflictionChecker consumes persisted conflict findings for the current
// intent version. It does not run geometry, overlap, or DSS logic.
type DeconflictionChecker struct{}

func (DeconflictionChecker) Name() string { return "deconfliction" }

func (DeconflictionChecker) Evaluate(_ context.Context, snapshot Snapshot, builder *Builder) {
	findings := currentConflictFindings(snapshot)
	if len(findings) == 0 {
		builder.Block(
			domain.PreflightCheckAirspace,
			deconflictionCheckKey,
			deconflictionSource,
			deconflictionRequirement,
			"current-version deconfliction evidence is required",
			"run deconfliction check for the current operational-intent version",
		)
		return
	}

	for _, finding := range findings {
		switch finding.Status {
		case domain.ConflictFindingStatusClear:
			continue
		case domain.ConflictFindingStatusPotentialConflict:
			builder.Block(
				domain.PreflightCheckAirspace,
				deconflictionCheckKey,
				deconflictionSource,
				deconflictionRequirement,
				"current-version deconfliction evidence has potential conflict",
				"resolve potential conflict or re-run deconfliction for the current intent version",
			)
			return
		case domain.ConflictFindingStatusIndeterminate:
			builder.Block(
				domain.PreflightCheckAirspace,
				deconflictionCheckKey,
				deconflictionSource,
				deconflictionRequirement,
				"current-version deconfliction evidence is indeterminate",
				"resolve indeterminate deconfliction evidence for the current intent version",
			)
			return
		case domain.ConflictFindingStatusConflict:
			builder.Block(
				domain.PreflightCheckAirspace,
				deconflictionCheckKey,
				deconflictionSource,
				deconflictionRequirement,
				"current-version deconfliction evidence has conflict",
				"resolve conflict or re-run deconfliction for the current intent version",
			)
			return
		default:
			builder.Block(
				domain.PreflightCheckAirspace,
				deconflictionCheckKey,
				deconflictionSource,
				deconflictionRequirement,
				"current-version deconfliction evidence has unrecognized status",
				"re-run deconfliction for the current intent version",
			)
			return
		}
	}

	builder.Clear(
		domain.PreflightCheckAirspace,
		deconflictionCheckKey,
		deconflictionSource,
		deconflictionRequirement,
		"current-version deconfliction evidence is clear",
	)
}

func currentConflictFindings(snapshot Snapshot) []domain.ConflictFinding {
	filtered := make([]domain.ConflictFinding, 0, len(snapshot.ConflictFindings))
	for _, finding := range snapshot.ConflictFindings {
		if finding.IntentID != snapshot.Intent.ID || finding.IntentVersion != snapshot.Intent.Version {
			continue
		}
		filtered = append(filtered, finding)
	}
	return filtered
}
