package deconfliction_test

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestAirspaceProviderFiltersCandidatesByLifecycleStatus(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target, targetVolumes := providerTargetIntent(t, ctx, store, now)

	for _, tc := range []struct {
		id     string
		status domain.IntentStatus
	}{
		{id: "intent-draft", status: domain.IntentStatusDraft},
		{id: "intent-submitted", status: domain.IntentStatusSubmitted},
		{id: "intent-review", status: domain.IntentStatusReview},
		{id: "intent-rejected", status: domain.IntentStatusRejected},
		{id: "intent-canceled", status: domain.IntentStatusCanceled},
		{id: "intent-complete", status: domain.IntentStatusComplete},
		{id: "intent-superseded", status: domain.IntentStatusSuperseded},
		{id: "intent-accepted", status: domain.IntentStatusAccepted},
		{id: "intent-active", status: domain.IntentStatusActive},
	} {
		intent := createAcceptedIntentWithVolume(t, ctx, store, now, tc.id, "aircraft-2", "volume-"+tc.id, squareGeoJSON(), 10, 120, now)
		intent.Status = tc.status
		must(t, store.UpdateOperationalIntent(ctx, intent))
	}

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, targetVolumes)
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	got := candidateIntentIDs(candidates)
	if len(got) != 2 || !got["intent-accepted"] || !got["intent-active"] {
		t.Fatalf("candidate intents = %v, want only accepted and active peers", got)
	}
}

func TestAirspaceProviderExcludesTargetIntent(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createAcceptedIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120, now)
	targetVolumes, err := store.ListOperationalVolumes(ctx, target.ID)
	must(t, err)

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, volumesForVersion(targetVolumes, target.Version))
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want target intent excluded from its own candidate set", candidates)
	}
}

func TestAirspaceProviderFiltersPeerVolumesByIntentVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target, targetVolumes := providerTargetIntent(t, ctx, store, now)
	peer := createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer-v1", squareGeoJSON(), 10, 120, now)
	peer.Version = 2
	must(t, store.UpdateOperationalIntent(ctx, peer))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-peer-v2",
		OperatorID:    "operator-1",
		IntentID:      peer.ID,
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, targetVolumes)
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || len(candidates[0].Volumes) != 1 || candidates[0].Volumes[0].ID != "volume-peer-v2" {
		t.Fatalf("candidates = %#v, want only the current-version peer volume", candidates)
	}
}

func TestAirspaceProviderFiltersPeerVolumesByTimeWindow(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target, targetVolumes := providerTargetIntent(t, ctx, store, now)
	// Target occupies [now, now+1h); the peer starts exactly when the target
	// ends, so the half-open windows do not overlap.
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now.Add(time.Hour))

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, targetVolumes)
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want time-disjoint peer volume filtered out", candidates)
	}
}

func TestAirspaceProviderFiltersPeerVolumesByAltitudeBand(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target, targetVolumes := providerTargetIntent(t, ctx, store, now)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 200, 300, now)

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, targetVolumes)
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want altitude-separated peer volume filtered out", candidates)
	}
}

func TestAirspaceProviderKeepsPeerVolumesWithMismatchedAltitudeReference(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target, targetVolumes := providerTargetIntent(t, ctx, store, now)
	// Altitude bands are disjoint, but the references differ (target is AGL),
	// so the bands cannot be compared locally and narrow-phase evaluation must
	// still see this volume to fail closed.
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 200, 300, now)
	peerVolumes, err := store.ListOperationalVolumes(ctx, "intent-peer")
	must(t, err)
	peerVolume := peerVolumes[0]
	peerVolume.AltitudeRef = domain.AltitudeReferenceMSL
	must(t, store.RecordOperationalVolume(ctx, peerVolume))

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, targetVolumes)
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || len(candidates[0].Volumes) != 1 {
		t.Fatalf("candidates = %#v, want mismatched-reference peer volume kept", candidates)
	}
}

func TestAirspaceProviderKeepsPeerVolumesWithUnusableDimensions(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target, targetVolumes := providerTargetIntent(t, ctx, store, now)
	// The peer volume is time-disjoint from the target, but its missing
	// altitude band means deconfliction must fail closed rather than filter.
	peer := createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now.Add(2*time.Hour))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-peer",
		OperatorID:    "operator-1",
		IntentID:      peer.ID,
		IntentVersion: peer.Version,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  0,
		MaxAltitudeM:  0,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now.Add(2 * time.Hour),
		EndsAt:        now.Add(3 * time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	candidates, err := deconfliction.NewLocalStoreAirspaceProvider(store).QueryConflictCandidates(ctx, target, targetVolumes)
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || len(candidates[0].Volumes) != 1 {
		t.Fatalf("candidates = %#v, want unusable-dimension peer volume kept for fail-closed evaluation", candidates)
	}
}

type stubAirspaceProvider struct {
	candidates []domain.OperationalIntentConflictCandidate
	calls      int
}

func (p *stubAirspaceProvider) QueryConflictCandidates(_ context.Context, _ domain.OperationalIntent, _ []domain.OperationalVolume) ([]domain.OperationalIntentConflictCandidate, error) {
	p.calls++
	return p.candidates, nil
}

func TestDeconflictionUsesInjectedAirspaceProvider(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	// This peer fully overlaps the target in the store, but the injected
	// provider does not surface it, proving CheckIntent no longer scans the
	// durable store for candidates.
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now)
	provider := &stubAirspaceProvider{}

	result, err := deconfliction.NewDeconflictionServiceWithClock(store, fixedClock(now), provider).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want candidate discovery delegated once", provider.calls)
	}
	if result.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("posture = %q, want clear when provider returns no candidates; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionEvaluatesProviderSuppliedCandidates(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	provider := &stubAirspaceProvider{
		candidates: []domain.OperationalIntentConflictCandidate{{
			Intent: domain.OperationalIntent{ID: "intent-remote", Version: 1, Status: domain.IntentStatusAccepted},
			Volumes: []domain.OperationalVolume{{
				ID:            "volume-remote",
				IntentID:      "intent-remote",
				IntentVersion: 1,
				GeoJSON:       squareGeoJSON(),
				MinAltitudeM:  10,
				MaxAltitudeM:  120,
				AltitudeRef:   domain.AltitudeReferenceAGL,
				StartsAt:      now,
				EndsAt:        now.Add(time.Hour),
			}},
		}},
	}

	result, err := deconfliction.NewDeconflictionServiceWithClock(store, fixedClock(now), provider).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("posture = %q, want potential_conflict from provider-supplied candidate; findings=%#v", result.Posture, result.Findings)
	}
	if len(result.Findings) != 1 || result.Findings[0].ConflictingIntentID != "intent-remote" || result.Findings[0].ConflictingVolumeID != "volume-remote" {
		t.Fatalf("findings = %#v, want single finding against provider-supplied volume", result.Findings)
	}
}

func providerTargetIntent(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time) (domain.OperationalIntent, []domain.OperationalVolume) {
	t.Helper()
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	volumes, err := store.ListOperationalVolumes(ctx, target.ID)
	must(t, err)
	return target, volumesForVersion(volumes, target.Version)
}

func candidateIntentIDs(candidates []domain.OperationalIntentConflictCandidate) map[string]bool {
	ids := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		ids[candidate.Intent.ID] = true
	}
	return ids
}
