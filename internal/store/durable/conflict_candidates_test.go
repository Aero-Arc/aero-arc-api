package durable

import (
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestPeerVolumeCouldConflictKeepsUnusableDimensions(t *testing.T) {
	target := domain.OperationalVolume{
		StartsAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		AltitudeRef:  domain.AltitudeReferenceAGL,
	}
	peer := domain.OperationalVolume{
		StartsAt: time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC),
	}
	if !PeerVolumeCouldConflict([]domain.OperationalVolume{target}, peer) {
		t.Fatal("unusable peer volume should be kept for fail-closed evaluation")
	}
}

func TestPeerVolumeCouldConflictKeepsMismatchedAltitudeReference(t *testing.T) {
	target := domain.OperationalVolume{
		StartsAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		AltitudeRef:  domain.AltitudeReferenceAGL,
	}
	peer := domain.OperationalVolume{
		StartsAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		MinAltitudeM: 200,
		MaxAltitudeM: 300,
		AltitudeRef:  domain.AltitudeReferenceMSL,
	}
	if !PeerVolumeCouldConflict([]domain.OperationalVolume{target}, peer) {
		t.Fatal("mismatched altitude reference should be kept")
	}
}

func TestFilterConflictCandidateVolumesKeepsUnusableDimensionsOutsideEnvelope(t *testing.T) {
	targets := []domain.OperationalVolume{{
		StartsAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		AltitudeRef:  domain.AltitudeReferenceAGL,
	}}
	peerVolumes := []domain.OperationalVolume{{
		StartsAt: time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC),
	}}
	filtered := FilterConflictCandidateVolumes(targets, peerVolumes)
	if len(filtered) != 1 {
		t.Fatalf("filtered = %#v, want unusable peer volume kept outside target time envelope", filtered)
	}
}

func TestFilterConflictCandidateVolumesUsesConservativeTimeEnvelope(t *testing.T) {
	targets := []domain.OperationalVolume{{
		StartsAt:     time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC),
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		AltitudeRef:  domain.AltitudeReferenceAGL,
	}}
	peerVolumes := []domain.OperationalVolume{{
		StartsAt:     time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC),
		EndsAt:       time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC),
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		AltitudeRef:  domain.AltitudeReferenceAGL,
	}}
	if filtered := FilterConflictCandidateVolumes(targets, peerVolumes); len(filtered) != 0 {
		t.Fatalf("filtered = %#v, want time-disjoint peer volume removed", filtered)
	}
}
