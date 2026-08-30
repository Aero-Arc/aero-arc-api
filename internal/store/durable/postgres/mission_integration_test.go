//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestMissionPersistsAcrossStoreRestartWithPostGISCoverage(t *testing.T) {
	ctx := context.Background()
	writer, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.pool.Exec(ctx, `TRUNCATE mission_deployments, mission_items, missions, flight_records, aircraft, received_peer_notifications, peer_notifications, operational_intent_publications, conflict_findings, operational_volumes, operational_intents`); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	intent := domain.OperationalIntent{
		ID: "mission-intent", Version: 1, AircraftID: "mission-aircraft",
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := writer.CreateOperationalIntent(ctx, intent); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.CreateAircraft(ctx, domain.Aircraft{ID: intent.AircraftID, OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.CreateFlightRecord(ctx, domain.FlightRecord{ID: "flight-1", OperatorID: "operator-1", AircraftID: intent.AircraftID, IntentID: intent.ID, IntentVersion: intent.Version, Status: domain.FlightStatusPlanned}); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	volume := domain.OperationalVolume{
		ID: "mission-volume", IntentID: intent.ID, IntentVersion: intent.Version,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: 0, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceMSL,
		StartsAt: now, EndsAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	if err := writer.RecordOperationalVolume(ctx, volume); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	mission := domain.Mission{
		ID: "mission-1", OperatorID: "operator-1", FlightID: "flight-1", AircraftID: intent.AircraftID,
		IntentID: intent.ID, IntentVersion: intent.Version,
		SourceFormat:       domain.MissionSourceFormatQGCWPL110,
		SourceSHA256:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		MissionDigest:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IdempotencyKey:     "mission-integration-key",
		IdempotencyRequest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CreatedAt:          now,
		Items: []domain.MissionItem{
			{Sequence: 0, Frame: 0, Command: 22, Autocontinue: true, LongitudeE7: -977500000, LatitudeE7: 352500000, AltitudeM: 20},
			{Sequence: 1, Frame: 0, Command: 21, Param4: 1, Autocontinue: true, LongitudeE7: -972500000, LatitudeE7: 357500000, AltitudeM: 10},
		},
	}
	stored, err := writer.CreateMission(ctx, mission)
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if stored.Version != 1 {
		writer.Close()
		t.Fatalf("version = %d", stored.Version)
	}
	deployment := domain.MissionDeployment{
		ID: "deployment-1", OperatorID: mission.OperatorID, FlightID: mission.FlightID,
		AircraftID: mission.AircraftID, AgentID: "agent-1", IntentID: mission.IntentID,
		IntentVersion: mission.IntentVersion, MissionID: mission.ID, MissionVersion: stored.Version,
		MissionDigest: mission.MissionDigest, CommandID: "mission-command-1", OperationContextCommandID: "context-command-1",
		IdempotencyKey: "deployment-integration-key", IdempotencyRequest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Status: domain.MissionDeploymentOutcomeUnknown, Message: "deadline exceeded", DispatchStarted: true, AttemptCount: 1,
		IssuedAt: now, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := writer.CreateMissionDeployment(ctx, deployment); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	coverage, err := writer.CheckMissionCoverage(ctx, volume, mission.Items)
	if err != nil || len(coverage.UncoveredItems) != 0 || len(coverage.UncoveredSegments) != 0 {
		writer.Close()
		t.Fatalf("coverage = %#v, err=%v", coverage, err)
	}
	writer.Close()

	reader, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	current, err := reader.GetCurrentMissionForFlight(ctx, mission.FlightID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != mission.ID || current.Version != 1 || len(current.Items) != 2 || current.Items[1].LongitudeE7 != -972500000 {
		t.Fatalf("restarted current mission = %#v", current)
	}
	restartedDeployment, err := reader.GetMissionDeployment(ctx, deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restartedDeployment.CommandID != deployment.CommandID || restartedDeployment.OperationContextCommandID != deployment.OperationContextCommandID ||
		restartedDeployment.Status != domain.MissionDeploymentOutcomeUnknown || !restartedDeployment.DispatchStarted || restartedDeployment.AgentID != "agent-1" {
		t.Fatalf("restarted deployment = %#v", restartedDeployment)
	}
	deploymentReplay, err := reader.CreateMissionDeployment(ctx, deployment)
	if err != nil || deploymentReplay.ID != deployment.ID {
		t.Fatalf("deployment replay = %#v err=%v", deploymentReplay, err)
	}
	deploymentConflict := deployment
	deploymentConflict.ID = "deployment-conflict"
	deploymentConflict.IdempotencyRequest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := reader.CreateMissionDeployment(ctx, deploymentConflict); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("deployment idempotency conflict = %v", err)
	}
	replayed, err := reader.CreateMission(ctx, mission)
	if err != nil || replayed.ID != mission.ID || replayed.Version != 1 {
		t.Fatalf("replayed = %#v, err=%v", replayed, err)
	}
	conflict := mission
	conflict.ID = "mission-conflict"
	conflict.IdempotencyRequest = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if _, err := reader.CreateMission(ctx, conflict); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	}

	outside := append([]domain.MissionItem(nil), mission.Items...)
	outside[1].LongitudeE7 = -965000000
	coverage, err = reader.CheckMissionCoverage(ctx, volume, outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage.UncoveredItems) != 1 || coverage.UncoveredItems[0] != 1 || len(coverage.UncoveredSegments) != 1 || coverage.UncoveredSegments[0] != 0 {
		t.Fatalf("outside coverage = %#v", coverage)
	}
}
