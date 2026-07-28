package deconfliction

import (
	"fmt"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestPreparePeerVolumesIncludesNominalAndOffNominalVolumes(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	record := airspaceprovider.OperationalIntent{
		Source: airspaceprovider.Source{ProviderID: "dss-one", ReferenceID: "reference-one"},
		Intent: domain.OperationalIntent{ID: "peer-intent", Version: 3},
		Volumes: []domain.OperationalVolume{
			conflictTestVolume("nominal", "peer-intent", 3, now),
		},
		OffNominalVolumes: []domain.OperationalVolume{
			conflictTestVolume("off-nominal", "peer-intent", 3, now),
		},
	}

	peers := preparePeerVolumes([]airspaceprovider.OperationalIntent{record})

	if len(peers) != 2 {
		t.Fatalf("prepared peers = %d, want 2", len(peers))
	}
	if peers[0].volume.ID != "nominal" || peers[1].volume.ID != "off-nominal" {
		t.Fatalf("prepared volume IDs = %q, %q", peers[0].volume.ID, peers[1].volume.ID)
	}
	for _, peer := range peers {
		if peer.status != domain.ConflictFindingStatusClear || !peer.dimensionsUsable {
			t.Fatalf("prepared peer = %#v, want usable clear volume", peer)
		}
		if peer.source.ReferenceID != record.Source.ReferenceID || peer.intent.ID != record.Intent.ID {
			t.Fatalf("prepared peer lost source or intent metadata: %#v", peer)
		}
	}
}

func TestEvaluateConflictsComparesEachTargetPeerPairOnce(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	target := domain.OperationalIntent{ID: "target-intent", Version: 1}
	targetVolumes := []domain.OperationalVolume{
		conflictTestVolume("target-one", target.ID, target.Version, now),
		conflictTestVolume("target-two", target.ID, target.Version, now),
	}
	record := airspaceprovider.OperationalIntent{
		Source: airspaceprovider.Source{ProviderID: "dss-one", ReferenceID: "reference-one"},
		Intent: domain.OperationalIntent{ID: "peer-intent", Version: 2},
		Volumes: []domain.OperationalVolume{
			conflictTestVolume("peer-one", "peer-intent", 2, now),
		},
	}

	findings := evaluateConflicts(target, targetVolumes, []airspaceprovider.OperationalIntent{record})

	if len(findings) != 2 {
		t.Fatalf("findings = %d, want one for each target-peer pair: %#v", len(findings), findings)
	}
	seenTargets := make(map[string]int)
	for _, finding := range findings {
		if finding.Status != domain.ConflictFindingStatusPotentialConflict {
			t.Fatalf("finding status = %q, want potential conflict", finding.Status)
		}
		if finding.ConflictingVolumeID != "peer-one" || finding.SourceID != "dss-one:reference-one" {
			t.Fatalf("finding lost peer provenance: %#v", finding)
		}
		seenTargets[finding.VolumeID]++
	}
	if seenTargets["target-one"] != 1 || seenTargets["target-two"] != 1 {
		t.Fatalf("findings by target = %#v, want one finding per target", seenTargets)
	}
}

func TestEvaluateConflictsFailsClosedForPeerWithInvalidDimensions(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	target := domain.OperationalIntent{ID: "target-intent", Version: 1}
	peerVolume := conflictTestVolume("peer-invalid", "peer-intent", 2, now)
	peerVolume.MaxAltitudeM = 0
	record := airspaceprovider.OperationalIntent{
		Source: airspaceprovider.Source{ProviderID: "dss-one", ReferenceID: "reference-one"},
		Intent: domain.OperationalIntent{ID: "peer-intent", Version: 2},
		Volumes: []domain.OperationalVolume{
			peerVolume,
		},
	}

	findings := evaluateConflicts(
		target,
		[]domain.OperationalVolume{conflictTestVolume("target-one", target.ID, target.Version, now)},
		[]airspaceprovider.OperationalIntent{record},
	)

	if len(findings) != 1 {
		t.Fatalf("findings = %d, want one indeterminate finding: %#v", len(findings), findings)
	}
	if findings[0].Status != domain.ConflictFindingStatusIndeterminate ||
		findings[0].ConflictingVolumeID != peerVolume.ID {
		t.Fatalf("finding = %#v, want indeterminate peer-volume finding", findings[0])
	}
}

func BenchmarkEvaluateConflictsFiveTargetsOneThousandPeers(b *testing.B) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	target := domain.OperationalIntent{ID: "target-intent", Version: 1}
	targetVolumes := make([]domain.OperationalVolume, 5)
	for index := range targetVolumes {
		targetVolumes[index] = conflictTestVolume(
			fmt.Sprintf("target-%d", index),
			target.ID,
			target.Version,
			now,
		)
	}

	records := make([]airspaceprovider.OperationalIntent, 1_000)
	for index := range records {
		intentID := fmt.Sprintf("peer-intent-%d", index)
		records[index] = airspaceprovider.OperationalIntent{
			Source: airspaceprovider.Source{
				ProviderID:  "benchmark-dss",
				ReferenceID: fmt.Sprintf("reference-%d", index),
				Version:     1,
			},
			Intent: domain.OperationalIntent{ID: intentID, Version: 1},
			Volumes: []domain.OperationalVolume{
				conflictTestVolume(fmt.Sprintf("peer-volume-%d", index), intentID, 1, now),
			},
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		findings := evaluateConflicts(target, targetVolumes, records)
		if len(findings) != 5_000 {
			b.Fatalf("findings = %d, want 5000", len(findings))
		}
	}
}

func conflictTestVolume(id, intentID string, intentVersion int, now time.Time) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID:            id,
		IntentID:      intentID,
		IntentVersion: intentVersion,
		GeoJSON:       `{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}`,
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceWGS84,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
	}
}
