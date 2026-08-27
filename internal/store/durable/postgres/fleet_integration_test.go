//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestAircraftFlightAndMissionFencePersistAcrossRestart(t *testing.T) {
	ctx := context.Background()
	writer, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	resetMissionFleetTables(t, writer)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "persist-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := writer.CreateAircraft(ctx, aircraft); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "persist-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := writer.CreateOperationalIntent(ctx, intent); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "persist-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: intent.Version, Status: domain.FlightStatusPlanned}
	if err := writer.CreateFlightRecord(ctx, flight); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	mission := postgresLifecycleMission(flight, "persist-mission", "persist-import-key")
	mission, err = writer.CreateMissionForPlannedFlight(ctx, mission)
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}
	deployment := postgresLifecycleDeployment(mission, "persist-deployment", "persist-deploy-key", domain.MissionDeploymentApplied)
	if _, err := writer.CreateMissionDeploymentForPlannedFlight(ctx, deployment); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()

	reader, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	restartedAircraft, err := reader.GetAircraft(ctx, aircraft.ID)
	if err != nil || restartedAircraft.AgentID != aircraft.AgentID {
		t.Fatalf("aircraft = %#v err=%v", restartedAircraft, err)
	}
	restartedFlight, err := reader.GetFlightRecord(ctx, flight.ID)
	if err != nil || restartedFlight.IntentVersion != flight.IntentVersion || restartedFlight.Status != domain.FlightStatusPlanned {
		t.Fatalf("flight = %#v err=%v", restartedFlight, err)
	}
	active := restartedFlight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	if err := reader.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	if replay, err := reader.CreateMissionForPlannedFlight(ctx, mission); err != nil || replay.ID != mission.ID {
		t.Fatalf("post-start import replay = %#v err=%v", replay, err)
	}
	if replay, err := reader.CreateMissionDeploymentForPlannedFlight(ctx, deployment); err != nil || replay.ID != deployment.ID {
		t.Fatalf("post-start deployment replay = %#v err=%v", replay, err)
	}
}

func TestPostgresFleetListAndUpdateRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	first := domain.Aircraft{ID: "fleet-aircraft-a", OperatorID: "operator-1", AgentID: "agent-a", CreatedAt: now, UpdatedAt: now}
	second := domain.Aircraft{ID: "fleet-aircraft-b", OperatorID: "operator-1", AgentID: "agent-b", CreatedAt: now, UpdatedAt: now}
	for _, aircraft := range []domain.Aircraft{second, first} {
		if err := store.CreateAircraft(ctx, aircraft); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateAircraft(ctx, first); !errors.Is(err, durable.ErrAlreadyExists) {
		t.Fatalf("duplicate aircraft error = %v", err)
	}
	aircraft, err := store.ListAircraft(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(aircraft) != 2 || aircraft[0].ID != first.ID || aircraft[1].ID != second.ID {
		t.Fatalf("aircraft = %#v", aircraft)
	}
	if _, err := store.GetAircraft(ctx, "missing-aircraft"); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("missing aircraft error = %v", err)
	}
	intent := domain.OperationalIntent{ID: "fleet-intent", Version: 1, OperatorID: "operator-1", AircraftID: first.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "fleet-flight", OperatorID: "operator-1", AircraftID: first.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned, StartedAt: now}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFlightRecord(ctx, flight); !errors.Is(err, durable.ErrAlreadyExists) {
		t.Fatalf("duplicate flight error = %v", err)
	}
	allFlights, err := store.ListFlightRecords(ctx, "")
	if err != nil || len(allFlights) != 1 || allFlights[0].ID != flight.ID {
		t.Fatalf("all flights = %#v err=%v", allFlights, err)
	}
	scopedFlights, err := store.ListFlightRecords(ctx, first.ID)
	if err != nil || len(scopedFlights) != 1 || scopedFlights[0].AircraftID != first.ID {
		t.Fatalf("scoped flights = %#v err=%v", scopedFlights, err)
	}
	emptyFlights, err := store.ListFlightRecords(ctx, second.ID)
	if err != nil || len(emptyFlights) != 0 {
		t.Fatalf("empty flights = %#v err=%v", emptyFlights, err)
	}
	updated := flight
	updated.Status = domain.FlightStatusAborted
	if err := store.UpdateFlightRecord(ctx, updated, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := store.GetFlightRecord(ctx, flight.ID)
	if err != nil || roundTrip.Status != domain.FlightStatusAborted {
		t.Fatalf("updated flight = %#v err=%v", roundTrip, err)
	}
	if err := store.UpdateFlightRecord(ctx, updated, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale flight update error = %v", err)
	}
	missing := updated
	missing.ID = "missing-flight"
	if err := store.UpdateFlightRecord(ctx, missing, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("missing flight update error = %v", err)
	}
}

func TestPostgresMissionDeploymentUpdatePersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	writer, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	resetMissionFleetTables(t, writer)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "deployment-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := writer.CreateAircraft(ctx, aircraft); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "deployment-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := writer.CreateOperationalIntent(ctx, intent); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "deployment-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned, StartedAt: now}
	if err := writer.CreateFlightRecord(ctx, flight); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	mission, err := writer.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "deployment-mission", "deployment-import-key"))
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}
	created, err := writer.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(mission, "deployment-record", "deployment-idempotency-key", domain.MissionDeploymentPending))
	if err != nil {
		writer.Close()
		t.Fatal(err)
	}
	replayed, err := writer.GetMissionDeploymentByIdempotencyKey(ctx, created.IdempotencyKey)
	if err != nil || replayed.ID != created.ID {
		writer.Close()
		t.Fatalf("deployment replay = %#v err=%v", replayed, err)
	}
	updated := created
	updated.Status = domain.MissionDeploymentOutcomeUnknown
	updated.Message = "relay deadline exceeded"
	updated.AttemptCount = 1
	updated.UpdatedAt = now.Add(time.Minute)
	if err := writer.UpdateMissionDeployment(ctx, updated, created.Revision); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err := writer.UpdateMissionDeployment(ctx, updated, created.Revision); !errors.Is(err, durable.ErrVersionConflict) {
		writer.Close()
		t.Fatalf("stale deployment update error = %v", err)
	}
	missing := updated
	missing.ID = "missing-deployment"
	if err := writer.UpdateMissionDeployment(ctx, missing, 0); !errors.Is(err, durable.ErrNotFound) {
		writer.Close()
		t.Fatalf("missing deployment update error = %v", err)
	}
	writer.Close()

	reader, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	persisted, err := reader.GetMissionDeployment(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Revision != 1 || persisted.Status != domain.MissionDeploymentOutcomeUnknown || persisted.AttemptCount != 1 || persisted.Message != updated.Message {
		t.Fatalf("persisted deployment = %#v", persisted)
	}
	if _, err := reader.GetMissionDeploymentByIdempotencyKey(ctx, "missing-deployment-key"); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("missing deployment replay error = %v", err)
	}
}

func TestPostgresMissionImportAndStartRaceIsSerialized(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "race-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAircraft(ctx, aircraft); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "race-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "race-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	current, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "race-current", "race-current-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(current, "race-applied", "race-applied-key", domain.MissionDeploymentApplied)); err != nil {
		t.Fatal(err)
	}
	candidate := postgresLifecycleMission(flight, "race-candidate", "race-candidate-key")
	active := flight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	ready := make(chan struct{})
	var group sync.WaitGroup
	var importErr, startErr error
	group.Add(2)
	go func() {
		defer group.Done()
		<-ready
		_, importErr = store.CreateMissionForPlannedFlight(ctx, candidate)
	}()
	go func() {
		defer group.Done()
		<-ready
		startErr = store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned)
	}()
	close(ready)
	group.Wait()
	if importErr == nil && startErr == nil {
		t.Fatal("mission import and flight start both committed")
	}
	if importErr != nil && !errors.Is(importErr, durable.ErrVersionConflict) {
		t.Fatalf("import error = %v", importErr)
	}
	if startErr != nil && !errors.Is(startErr, durable.ErrVersionConflict) {
		t.Fatalf("start error = %v", startErr)
	}
}

func TestPostgresMissionDeploymentAndStartRaceIsSerialized(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "deploy-race-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAircraft(ctx, aircraft); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "deploy-race-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "deploy-race-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	current, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "deploy-race-mission", "deploy-race-import-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(current, "deploy-race-applied", "deploy-race-applied-key", domain.MissionDeploymentApplied)); err != nil {
		t.Fatal(err)
	}
	candidate := postgresLifecycleDeployment(current, "deploy-race-pending", "deploy-race-pending-key", domain.MissionDeploymentPending)
	active := flight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	ready := make(chan struct{})
	var group sync.WaitGroup
	var createErr, startErr error
	group.Add(2)
	go func() {
		defer group.Done()
		<-ready
		_, createErr = store.CreateMissionDeploymentForPlannedFlight(ctx, candidate)
	}()
	go func() {
		defer group.Done()
		<-ready
		startErr = store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned)
	}()
	close(ready)
	group.Wait()
	if createErr == nil && startErr == nil {
		t.Fatal("mission deployment and flight start both committed")
	}
	if createErr != nil && !errors.Is(createErr, durable.ErrVersionConflict) {
		t.Fatalf("create error = %v", createErr)
	}
	if startErr != nil && !errors.Is(startErr, durable.ErrVersionConflict) {
		t.Fatalf("start error = %v", startErr)
	}
}

func TestPostgresStartScopesUncertaintyToExactCurrentMission(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "scope-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAircraft(ctx, aircraft); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "scope-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "scope-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	oldMission, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "scope-old", "scope-old-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(oldMission, "scope-old-unknown", "scope-old-unknown-key", domain.MissionDeploymentOutcomeUnknown)); err != nil {
		t.Fatal(err)
	}
	current, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "scope-current", "scope-current-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(current, "scope-current-applied", "scope-current-applied-key", domain.MissionDeploymentApplied)); err != nil {
		t.Fatal(err)
	}
	active := flight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
		t.Fatalf("historical uncertainty blocked verified current mission: %v", err)
	}
}

func resetMissionFleetTables(t *testing.T, store *Store) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE mission_deployments, mission_items, missions, flight_records, aircraft, received_peer_notifications, peer_notifications, operational_intent_publications, conflict_findings, operational_volumes, operational_intents`); err != nil {
		t.Fatal(err)
	}
}

func postgresLifecycleMission(flight domain.FlightRecord, id, key string) domain.Mission {
	return domain.Mission{
		ID: id, OperatorID: flight.OperatorID, FlightID: flight.ID, AircraftID: flight.AircraftID, IntentID: flight.IntentID, IntentVersion: flight.IntentVersion,
		SourceFormat: domain.MissionSourceFormatQGCWPL110, SourceSHA256: strings.Repeat("a", 64), MissionDigest: strings.Repeat("b", 64),
		IdempotencyKey: key, IdempotencyRequest: strings.Repeat("c", 64), CreatedAt: time.Now().UTC(),
		Items: []domain.MissionItem{{Sequence: 0, Frame: 0, Command: 22, Autocontinue: true}},
	}
}

func postgresLifecycleDeployment(mission domain.Mission, id, key string, status domain.MissionDeploymentStatus) domain.MissionDeployment {
	return domain.MissionDeployment{
		ID: id, OperatorID: mission.OperatorID, FlightID: mission.FlightID, AircraftID: mission.AircraftID, AgentID: "agent-1",
		IntentID: mission.IntentID, IntentVersion: mission.IntentVersion, MissionID: mission.ID, MissionVersion: mission.Version, MissionDigest: mission.MissionDigest,
		CommandID: id + "-command", OperationContextCommandID: id + "-context", IdempotencyKey: key,
		IdempotencyRequest: strings.Repeat("d", 64), Status: status, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Minute).UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
}
