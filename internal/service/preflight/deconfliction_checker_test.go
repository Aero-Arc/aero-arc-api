package preflight

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func conflictSnapshot(findings ...domain.ConflictFinding) Snapshot {
	snapshot := testSnapshot(timeNow())
	snapshot.Intent.ID = "intent-1"
	snapshot.Intent.Version = 2
	snapshot.ConflictFindings = findings
	return snapshot
}

func clearFinding(intentID string, version int, id string) domain.ConflictFinding {
	return domain.ConflictFinding{
		ID:            id,
		IntentID:      intentID,
		IntentVersion: version,
		Status:        domain.ConflictFindingStatusClear,
		SourceType:    domain.ConflictFindingSourceLocal,
		SourceID:      "deconfliction_service",
		EvaluatedAt:   timeNow(),
	}
}

func TestDeconflictionCheckerClearPasses(t *testing.T) {
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot(clearFinding("intent-1", 2, "finding-clear")))
	check := requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", false)
	if check.Summary != "current-version deconfliction evidence is clear" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestDeconflictionCheckerPotentialConflictBlocks(t *testing.T) {
	finding := clearFinding("intent-1", 2, "finding-potential")
	finding.Status = domain.ConflictFindingStatusPotentialConflict
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot(finding))
	requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", true)
}

func TestDeconflictionCheckerIndeterminateBlocks(t *testing.T) {
	finding := clearFinding("intent-1", 2, "finding-indeterminate")
	finding.Status = domain.ConflictFindingStatusIndeterminate
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot(finding))
	requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", true)
}

func TestDeconflictionCheckerMissingEvidenceBlocks(t *testing.T) {
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot())
	check := requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", true)
	if check.Summary != "current-version deconfliction evidence is required" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestDeconflictionCheckerOldVersionClearDoesNotSatisfy(t *testing.T) {
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot(clearFinding("intent-1", 1, "finding-old-clear")))
	requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", true)
}

func TestDeconflictionCheckerOldVersionBlockIgnoredWhenCurrentClear(t *testing.T) {
	oldBlock := clearFinding("intent-1", 1, "finding-old-block")
	oldBlock.Status = domain.ConflictFindingStatusPotentialConflict
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot(oldBlock, clearFinding("intent-1", 2, "finding-current-clear")))
	requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", false)
}

func TestDeconflictionCheckerOtherIntentIgnored(t *testing.T) {
	other := clearFinding("intent-other", 2, "finding-other")
	other.Status = domain.ConflictFindingStatusPotentialConflict
	builder := evaluateChecker(t, DeconflictionChecker{}, conflictSnapshot(other))
	requireCheck(t, builder, "deconfliction_current", "DECONFLICT-CURRENT", "deconfliction_service", true)
}

func TestDeconflictionCheckerRepeatedEvaluationDeterministic(t *testing.T) {
	snapshot := conflictSnapshot(clearFinding("intent-1", 2, "finding-clear"))
	first := evaluateChecker(t, DeconflictionChecker{}, snapshot)
	second := evaluateChecker(t, DeconflictionChecker{}, snapshot)
	if first.Checks()[0].ID != second.Checks()[0].ID || first.Checks()[0].Summary != second.Checks()[0].Summary {
		t.Fatalf("non-deterministic checks: %#v vs %#v", first.Checks()[0], second.Checks()[0])
	}
}

func TestLoadSnapshotScopesConflictFindingsToCurrentVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-1", AircraftID: "aircraft-1", Version: 2,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordConflictFinding(ctx, clearFinding("intent-1", 1, "old")); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordConflictFinding(ctx, clearFinding("intent-other", 2, "other")); err != nil {
		t.Fatal(err)
	}
	current := clearFinding("intent-1", 2, "current")
	if err := store.RecordConflictFinding(ctx, current); err != nil {
		t.Fatal(err)
	}

	snapshot, err := NewPreflightServiceWithClock(store, func() time.Time { return now }).loadSnapshot(ctx, "intent-1")
	if err != nil {
		t.Fatalf("loadSnapshot: %v", err)
	}
	if len(snapshot.ConflictFindings) != 1 || snapshot.ConflictFindings[0].ID != "current" {
		t.Fatalf("ConflictFindings = %#v, want only current", snapshot.ConflictFindings)
	}
}
