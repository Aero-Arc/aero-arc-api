package deconfliction

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// AirspaceAwarenessProvider performs broad-phase candidate discovery for
// deconfliction: given a target intent and its operational volumes, it returns
// the peer intents (and the subset of their volumes) that could plausibly
// conflict. Narrow-phase overlap evaluation remains the responsibility of the
// DeconflictionService, so providers may over-include candidates but must
// never exclude a volume that could produce a finding.
//
// This is the seam for future DSS/USS-backed discovery (for example a
// DSSAirspaceProvider or a CompositeAirspaceProvider).
type AirspaceAwarenessProvider interface {
	QueryConflictCandidates(
		ctx context.Context,
		intent domain.OperationalIntent,
		volumes []domain.OperationalVolume,
	) ([]domain.OperationalIntentConflictCandidate, error)
}
