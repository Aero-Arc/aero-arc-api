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
	restoredDeployment, err := reader.GetCurrentMissionDeploymentForFlight(ctx, flight.ID)
	if err != nil || restoredDeployment.ID != deployment.ID {
		t.Fatalf("restored deployment = %#v err=%v, want %s", restoredDeployment, err, deployment.ID)
	}
	for name, get := range map[string]func() (domain.Mission, error){
		"identity":    func() (domain.Mission, error) { return reader.GetMission(ctx, mission.ID) },
		"idempotency": func() (domain.Mission, error) { return reader.GetMissionByIdempotencyKey(ctx, mission.IdempotencyKey) },
		"binding": func() (domain.Mission, error) {
			return reader.GetCurrentMissionForIntent(ctx, mission.AircraftID, mission.IntentID, mission.IntentVersion)
		},
	} {
		got, getErr := get()
		if getErr != nil || got.ID != mission.ID || len(got.Items) != len(mission.Items) {
			t.Fatalf("%s mission = %#v err=%v", name, got, getErr)
		}
	}
	if _, err := reader.GetMission(ctx, "missing-mission"); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("missing mission error = %v", err)
	}
	active := restartedFlight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	if err := reader.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	commanded, err := reader.GetDeployedMissionForActiveFlight(ctx, aircraft.ID, intent.ID, intent.Version)
	if err != nil || commanded.ID != mission.ID {
		t.Fatalf("commanded mission = %#v err=%v, want %s", commanded, err, mission.ID)
	}
	futureFlight := flight
	futureFlight.ID = "persist-future-flight"
	if err := reader.CreateFlightRecord(ctx, futureFlight); err != nil {
		t.Fatal(err)
	}
	futureMission, err := reader.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(futureFlight, "persist-future-mission", "persist-future-import-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(futureMission, "persist-future-deployment", "persist-future-deploy-key", domain.MissionDeploymentApplied)); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("future deployment against active aircraft error = %v", err)
	}
	commanded, err = reader.GetDeployedMissionForActiveFlight(ctx, aircraft.ID, intent.ID, intent.Version)
	if err != nil || commanded.ID != mission.ID {
		t.Fatalf("commanded mission after future import = %#v err=%v, want %s", commanded, err, mission.ID)
	}
	futureActive := futureFlight
	futureActive.Status, futureActive.StartedAt = domain.FlightStatusActive, now.Add(2*time.Minute)
	if err := reader.StartFlightWithCurrentMissionDeployment(ctx, futureActive, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("second active flight error = %v, want version conflict", err)
	}
	if replay, err := reader.CreateMissionForPlannedFlight(ctx, mission); err != nil || replay.ID != mission.ID {
		t.Fatalf("post-start import replay = %#v err=%v", replay, err)
	}
	if replay, err := reader.CreateMissionDeploymentForPlannedFlight(ctx, deployment); err != nil || replay.ID != deployment.ID {
		t.Fatalf("post-start deployment replay = %#v err=%v", replay, err)
	}
	conflictingMission := mission
	conflictingMission.IdempotencyRequest = strings.Repeat("e", 64)
	if _, err := reader.CreateMissionForPlannedFlight(ctx, conflictingMission); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("post-start mission idempotency conflict = %v", err)
	}
	newMission := mission
	newMission.ID, newMission.IdempotencyKey, newMission.IdempotencyRequest = "post-start-mission", "post-start-import-key", strings.Repeat("f", 64)
	if _, err := reader.CreateMissionForPlannedFlight(ctx, newMission); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("post-start new mission error = %v", err)
	}
	conflictingDeployment := deployment
	conflictingDeployment.IdempotencyRequest = strings.Repeat("e", 64)
	if _, err := reader.CreateMissionDeploymentForPlannedFlight(ctx, conflictingDeployment); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("post-start deployment idempotency conflict = %v", err)
	}
	newDeployment := deployment
	newDeployment.ID, newDeployment.IdempotencyKey, newDeployment.IdempotencyRequest = "post-start-deployment", "post-start-deploy-key", strings.Repeat("f", 64)
	if _, err := reader.CreateMissionDeploymentForPlannedFlight(ctx, newDeployment); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("post-start new deployment error = %v", err)
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

func TestPostgresMissionImportAndIntentTransitionAreSerialized(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "intent-race-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAircraft(ctx, aircraft); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "intent-race-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "intent-race-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: intent.Version, Status: domain.FlightStatusPlanned}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}

	// Hold an uncommitted terminal transition under the same intent lock used by
	// mission creation. The concurrent import must wait, observe the committed
	// terminal state, and fail without inserting a mission.
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		t.Fatal(err)
	}
	terminal := intent
	terminal.Status = domain.IntentStatusComplete
	completedAt := now.Add(time.Minute)
	terminal.CompletedAt = &completedAt
	terminal.UpdatedAt = completedAt
	if err := updateOperationalIntentTx(ctx, tx, terminal, intent.Revision); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, createErr := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "intent-race-mission", "intent-race-key"))
		result <- createErr
	}()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if createErr := <-result; !errors.Is(createErr, durable.ErrVersionConflict) {
		t.Fatalf("mission import after concurrent terminal transition error = %v", createErr)
	}
	if _, err := store.GetCurrentMissionForFlight(ctx, flight.ID); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("mission persisted across terminal intent fence: %v", err)
	}
}

func TestMissionDeploymentCreationOrderMigratesExistingRows(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "order-migration-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAircraft(ctx, aircraft); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "order-migration-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "order-migration-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	mission, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "order-migration-mission", "order-migration-mission-key"))
	if err != nil {
		t.Fatal(err)
	}
	first := postgresLifecycleDeployment(mission, "order-migration-first", "order-migration-first-key", domain.MissionDeploymentApplied)
	first.CreatedAt = now
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := postgresLifecycleDeployment(mission, "order-migration-second", "order-migration-second-key", domain.MissionDeploymentRejected)
	second.CreatedAt = now.Add(time.Minute)
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `DROP INDEX IF EXISTS mission_deployments_flight_order_idx; ALTER TABLE mission_deployments DROP COLUMN creation_order`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, schema); err != nil {
		t.Fatalf("reapply schema migration: %v", err)
	}
	var firstOrder, secondOrder int64
	if err := store.pool.QueryRow(ctx, `SELECT creation_order FROM mission_deployments WHERE id=$1`, first.ID).Scan(&firstOrder); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT creation_order FROM mission_deployments WHERE id=$1`, second.ID).Scan(&secondOrder); err != nil {
		t.Fatal(err)
	}
	if firstOrder <= 0 || secondOrder <= firstOrder {
		t.Fatalf("backfilled creation order = first:%d second:%d", firstOrder, secondOrder)
	}
}

func TestPostgresAircraftMissionLifecycleUsesLatestAuthoritativeDeployment(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	setup := func(t *testing.T, prefix string) (domain.FlightRecord, domain.Mission) {
		t.Helper()
		resetMissionFleetTables(t, store)
		now := time.Now().UTC()
		aircraft := domain.Aircraft{ID: prefix + "-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
		if err := store.CreateAircraft(ctx, aircraft); err != nil {
			t.Fatal(err)
		}
		intent := domain.OperationalIntent{ID: prefix + "-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
		if err := store.CreateOperationalIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
		flight := domain.FlightRecord{ID: prefix + "-flight-1", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: intent.Version, Status: domain.FlightStatusPlanned}
		if err := store.CreateFlightRecord(ctx, flight); err != nil {
			t.Fatal(err)
		}
		mission, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, prefix+"-mission-1", prefix+"-mission-key-1"))
		if err != nil {
			t.Fatal(err)
		}
		return flight, mission
	}
	addFlight := func(t *testing.T, first domain.FlightRecord, prefix string) (domain.FlightRecord, domain.Mission) {
		t.Helper()
		flight := first
		flight.ID = prefix + "-flight-2"
		if err := store.CreateFlightRecord(ctx, flight); err != nil {
			t.Fatal(err)
		}
		mission, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, prefix+"-mission-2", prefix+"-mission-key-2"))
		if err != nil {
			t.Fatal(err)
		}
		return flight, mission
	}

	t.Run("newer mismatch invalidates older success", func(t *testing.T) {
		flight, mission := setup(t, "mismatch")
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(mission, "mismatch-older-applied", "mismatch-older-key", domain.MissionDeploymentApplied)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(mission, "mismatch-newer-result", "mismatch-newer-key", domain.MissionDeploymentOnboardMissionMismatch)); err != nil {
			t.Fatal(err)
		}
		active := flight
		active.Status = domain.FlightStatusActive
		if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("start after newer mismatch error = %v", err)
		}
	})

	t.Run("newer deployment for another flight invalidates older success", func(t *testing.T) {
		flight, mission := setup(t, "cross-latest")
		firstDeployment := postgresLifecycleDeployment(mission, "cross-latest-first-applied", "cross-latest-first-key", domain.MissionDeploymentApplied)
		firstDeployment.CreatedAt = time.Now().UTC()
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, firstDeployment); err != nil {
			t.Fatal(err)
		}
		_, secondMission := addFlight(t, flight, "cross-latest")
		secondDeployment := postgresLifecycleDeployment(secondMission, "cross-latest-second-applied", "cross-latest-second-key", domain.MissionDeploymentApplied)
		secondDeployment.CreatedAt = firstDeployment.CreatedAt.Add(-time.Hour)
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, secondDeployment); err != nil {
			t.Fatal(err)
		}
		active := flight
		active.Status = domain.FlightStatusActive
		if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("start after another flight deployment error = %v", err)
		}
	})

	t.Run("active flight blocks another flight deployment", func(t *testing.T) {
		flight, mission := setup(t, "active-fence")
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(mission, "active-fence-applied", "active-fence-applied-key", domain.MissionDeploymentApplied)); err != nil {
			t.Fatal(err)
		}
		_, secondMission := addFlight(t, flight, "active-fence")
		active := flight
		active.Status = domain.FlightStatusActive
		if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(secondMission, "active-fence-unsafe", "active-fence-unsafe-key", domain.MissionDeploymentPending)); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("deployment against active aircraft error = %v", err)
		}
	})

	t.Run("cross-flight deployment and start serialize", func(t *testing.T) {
		flight, mission := setup(t, "cross-race")
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(mission, "cross-race-applied", "cross-race-applied-key", domain.MissionDeploymentApplied)); err != nil {
			t.Fatal(err)
		}
		_, secondMission := addFlight(t, flight, "cross-race")
		candidate := postgresLifecycleDeployment(secondMission, "cross-race-candidate", "cross-race-candidate-key", domain.MissionDeploymentPending)
		active := flight
		active.Status = domain.FlightStatusActive
		ready := make(chan struct{})
		var group sync.WaitGroup
		var deploymentErr, startErr error
		group.Add(2)
		go func() {
			defer group.Done()
			<-ready
			_, deploymentErr = store.CreateMissionDeploymentForPlannedFlight(ctx, candidate)
		}()
		go func() {
			defer group.Done()
			<-ready
			startErr = store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned)
		}()
		close(ready)
		group.Wait()
		if deploymentErr == nil && startErr == nil {
			t.Fatal("cross-flight deployment and start both committed")
		}
		if deploymentErr != nil && !errors.Is(deploymentErr, durable.ErrVersionConflict) {
			t.Fatalf("deployment error = %v", deploymentErr)
		}
		if startErr != nil && !errors.Is(startErr, durable.ErrVersionConflict) {
			t.Fatalf("start error = %v", startErr)
		}
	})

	t.Run("deployment and replacement mission serialize", func(t *testing.T) {
		flight, mission := setup(t, "binding-race-mission")
		deployment := postgresLifecycleDeployment(mission, "binding-race-deployment", "binding-race-deployment-key", domain.MissionDeploymentPending)
		replacement := postgresLifecycleMission(flight, "binding-race-replacement", "binding-race-replacement-key")
		ready := make(chan struct{})
		var group sync.WaitGroup
		var deploymentErr, replacementErr error
		group.Add(2)
		go func() {
			defer group.Done()
			<-ready
			_, deploymentErr = store.CreateMissionDeploymentForPlannedFlight(ctx, deployment)
		}()
		go func() {
			defer group.Done()
			<-ready
			_, replacementErr = store.CreateMissionForPlannedFlight(ctx, replacement)
		}()
		close(ready)
		group.Wait()
		assertPostgresMissionBindingRace(t, deploymentErr, replacementErr)
	})

	t.Run("deployment and terminal intent transition serialize", func(t *testing.T) {
		flight, mission := setup(t, "binding-race-intent")
		deployment := postgresLifecycleDeployment(mission, "binding-race-intent-deployment", "binding-race-intent-deployment-key", domain.MissionDeploymentPending)
		intent, err := store.GetOperationalIntent(ctx, flight.IntentID)
		if err != nil {
			t.Fatal(err)
		}
		intent.Status = domain.IntentStatusComplete
		completedAt := time.Now().UTC()
		intent.CompletedAt = &completedAt
		intent.UpdatedAt = completedAt
		ready := make(chan struct{})
		var group sync.WaitGroup
		var deploymentErr, transitionErr error
		group.Add(2)
		go func() {
			defer group.Done()
			<-ready
			_, deploymentErr = store.CreateMissionDeploymentForPlannedFlight(ctx, deployment)
		}()
		go func() {
			defer group.Done()
			<-ready
			transitionErr = store.UpdateOperationalIntent(ctx, intent, intent.Revision)
		}()
		close(ready)
		group.Wait()
		assertPostgresMissionBindingRace(t, deploymentErr, transitionErr)
	})

	t.Run("outcome unknown fence survives reconciliation deadline", func(t *testing.T) {
		flight, mission := setup(t, "expired-unknown")
		deployment := postgresLifecycleDeployment(mission, "expired-unknown-deployment", "expired-unknown-deployment-key", domain.MissionDeploymentOutcomeUnknown)
		deployment.ExpiresAt = time.Now().Add(-time.Hour)
		deployment.ReconcileUntil = time.Now().Add(-time.Minute)
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, deployment); err != nil {
			t.Fatal(err)
		}
		replacement := postgresLifecycleMission(flight, "expired-unknown-replacement", "expired-unknown-replacement-key")
		if _, err := store.CreateMissionForPlannedFlight(ctx, replacement); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("replacement after reconciliation deadline error = %v", err)
		}
		intent, err := store.GetOperationalIntent(ctx, flight.IntentID)
		if err != nil {
			t.Fatal(err)
		}
		intent.Status = domain.IntentStatusComplete
		if err := store.UpdateOperationalIntent(ctx, intent, intent.Revision); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("terminal intent after reconciliation deadline error = %v", err)
		}
		active := flight
		active.Status = domain.FlightStatusActive
		if err := store.UpdateFlightRecord(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("flight mutation after reconciliation deadline error = %v", err)
		}
	})
}

func assertPostgresMissionBindingRace(t *testing.T, deploymentErr, mutationErr error) {
	t.Helper()
	if deploymentErr == nil && mutationErr == nil {
		t.Fatal("mission deployment and binding mutation both committed")
	}
	if deploymentErr != nil && mutationErr != nil {
		t.Fatalf("mission deployment and binding mutation both failed: deployment=%v mutation=%v", deploymentErr, mutationErr)
	}
	if deploymentErr != nil && !errors.Is(deploymentErr, durable.ErrVersionConflict) {
		t.Fatalf("deployment error = %v", deploymentErr)
	}
	if mutationErr != nil && !errors.Is(mutationErr, durable.ErrVersionConflict) {
		t.Fatalf("binding mutation error = %v", mutationErr)
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

func TestPostgresStartScopesTerminalHistoryToExactCurrentMission(t *testing.T) {
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
	oldDeployment, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(oldMission, "scope-old-unknown", "scope-old-unknown-key", domain.MissionDeploymentOutcomeUnknown))
	if err != nil {
		t.Fatal(err)
	}
	oldDeployment.Status = domain.MissionDeploymentRejected
	oldDeployment.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateMissionDeployment(ctx, oldDeployment, oldDeployment.Revision); err != nil {
		t.Fatal(err)
	}
	current, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "scope-current", "scope-current-key"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(current, "scope-current-pending", "scope-current-pending-key", domain.MissionDeploymentPending))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(current, "scope-second-pending", "scope-second-pending-key", domain.MissionDeploymentPending)); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("second unresolved deployment error = %v, want version conflict", err)
	}
	active := flight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("pending current deployment start error = %v", err)
	}
	pending.Status = domain.MissionDeploymentRejected
	pending.UpdatedAt = now.Add(30 * time.Second)
	if err := store.UpdateMissionDeployment(ctx, pending, pending.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(current, "scope-current-applied", "scope-current-applied-key", domain.MissionDeploymentApplied)); err != nil {
		t.Fatal(err)
	}
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
		t.Fatalf("historical terminal result blocked verified current mission: %v", err)
	}
}

func TestPostgresStartRejectsTerminalIntentInsideFence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	resetMissionFleetTables(t, store)
	now := time.Now().UTC()
	aircraft := domain.Aircraft{ID: "terminal-aircraft", OperatorID: "operator-1", AgentID: "agent-1", CreatedAt: now, UpdatedAt: now}
	if err := store.CreateAircraft(ctx, aircraft); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "terminal-intent", Version: 1, OperatorID: "operator-1", AircraftID: aircraft.ID, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "terminal-flight", OperatorID: "operator-1", AircraftID: aircraft.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned, StartedAt: now}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	active := flight
	active.Status, active.StartedAt = domain.FlightStatusActive, now.Add(time.Minute)
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("missing mission start error = %v", err)
	}
	mission, err := store.CreateMissionForPlannedFlight(ctx, postgresLifecycleMission(flight, "terminal-mission", "terminal-import-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("unverified mission start error = %v", err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, postgresLifecycleDeployment(mission, "terminal-applied", "terminal-deploy-key", domain.MissionDeploymentApplied)); err != nil {
		t.Fatal(err)
	}
	intent.Status = domain.IntentStatusComplete
	intent.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateOperationalIntent(ctx, intent, intent.Revision); err != nil {
		t.Fatal(err)
	}
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("terminal intent start error = %v", err)
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
