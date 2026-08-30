package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestMissionLifecycleFenceReplaysCommittedRequestsAfterStart(t *testing.T) {
	store, flight, mission := missionLifecycleStore(t)
	ctx := context.Background()
	stored, err := store.CreateMissionForPlannedFlight(ctx, mission)
	if err != nil {
		t.Fatal(err)
	}
	deployment := lifecycleDeployment(stored, "deployment-1", "deployment-key", domain.MissionDeploymentApplied)
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	active := flight
	active.Status = domain.FlightStatusActive
	active.StartedAt = time.Now().UTC()
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	replayedMission, err := store.CreateMissionForPlannedFlight(ctx, mission)
	if err != nil || replayedMission.ID != stored.ID {
		t.Fatalf("mission replay = %#v err=%v", replayedMission, err)
	}
	replayedDeployment, err := store.CreateMissionDeploymentForPlannedFlight(ctx, deployment)
	if err != nil || replayedDeployment.ID != deployment.ID {
		t.Fatalf("deployment replay = %#v err=%v", replayedDeployment, err)
	}
	conflict := mission
	conflict.ID = "mission-conflict"
	conflict.IdempotencyRequest = "different-request"
	if _, err := store.CreateMissionForPlannedFlight(ctx, conflict); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
	newMission := mission
	newMission.ID, newMission.IdempotencyKey = "mission-new", "mission-key-new"
	if _, err := store.CreateMissionForPlannedFlight(ctx, newMission); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("new active-flight import error = %v", err)
	}
}

func TestMissionImportFenceRejectsTerminalAndSupersededIntent(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(domain.OperationalIntent) domain.OperationalIntent
	}{
		{
			name: "terminal intent",
			mutate: func(intent domain.OperationalIntent) domain.OperationalIntent {
				intent.Status = domain.IntentStatusComplete
				return intent
			},
		},
		{
			name: "superseded intent version",
			mutate: func(intent domain.OperationalIntent) domain.OperationalIntent {
				intent.Version++
				intent.Status = domain.IntentStatusAccepted
				return intent
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, flight, mission := missionLifecycleStore(t)
			ctx := context.Background()
			intent, err := store.GetOperationalIntent(ctx, flight.IntentID)
			if err != nil {
				t.Fatal(err)
			}
			intent = test.mutate(intent)
			if intent.Version == flight.IntentVersion {
				if err := store.UpdateOperationalIntent(ctx, intent, intent.Revision); err != nil {
					t.Fatal(err)
				}
			} else if err := store.CreateOperationalIntent(ctx, intent); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateMissionForPlannedFlight(ctx, mission); !errors.Is(err, durable.ErrVersionConflict) {
				t.Fatalf("mission import error = %v", err)
			}
		})
	}
}

func TestStartFlightScopesUncertaintyToExactCurrentMission(t *testing.T) {
	store, flight, mission := missionLifecycleStore(t)
	ctx := context.Background()
	oldMission, err := store.CreateMissionForPlannedFlight(ctx, mission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, lifecycleDeployment(oldMission, "old-unknown", "old-unknown-key", domain.MissionDeploymentOutcomeUnknown)); err != nil {
		t.Fatal(err)
	}
	newMission := mission
	newMission.ID, newMission.IdempotencyKey, newMission.IdempotencyRequest = "mission-2", "mission-key-2", "mission-request-2"
	current, err := store.CreateMissionForPlannedFlight(ctx, newMission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, lifecycleDeployment(current, "current-applied", "current-applied-key", domain.MissionDeploymentApplied)); err != nil {
		t.Fatal(err)
	}
	active := flight
	active.Status = domain.FlightStatusActive
	if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); err != nil {
		t.Fatalf("historical uncertainty blocked verified current mission: %v", err)
	}
}

func TestDeploymentFenceRejectsSupersededMission(t *testing.T) {
	store, _, mission := missionLifecycleStore(t)
	ctx := context.Background()
	superseded, err := store.CreateMissionForPlannedFlight(ctx, mission)
	if err != nil {
		t.Fatal(err)
	}
	current := mission
	current.ID, current.IdempotencyKey, current.IdempotencyRequest = "mission-2", "mission-key-2", "mission-request-2"
	if _, err := store.CreateMissionForPlannedFlight(ctx, current); err != nil {
		t.Fatal(err)
	}
	deployment := lifecycleDeployment(superseded, "stale-deployment", "stale-deployment-key", domain.MissionDeploymentPending)
	if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, deployment); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("superseded mission deployment error = %v", err)
	}
}

func TestStartFlightBlocksCurrentUncertaintyAndTerminalIntent(t *testing.T) {
	t.Run("current uncertainty", func(t *testing.T) {
		store, flight, mission := missionLifecycleStore(t)
		ctx := context.Background()
		current, err := store.CreateMissionForPlannedFlight(ctx, mission)
		if err != nil {
			t.Fatal(err)
		}
		for _, deployment := range []domain.MissionDeployment{
			lifecycleDeployment(current, "applied", "applied-key", domain.MissionDeploymentApplied),
			lifecycleDeployment(current, "unknown", "unknown-key", domain.MissionDeploymentOutcomeUnknown),
		} {
			if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, deployment); err != nil {
				t.Fatal(err)
			}
		}
		active := flight
		active.Status = domain.FlightStatusActive
		if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("start error = %v", err)
		}
	})
	t.Run("terminal intent", func(t *testing.T) {
		store, flight, mission := missionLifecycleStore(t)
		ctx := context.Background()
		current, err := store.CreateMissionForPlannedFlight(ctx, mission)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, lifecycleDeployment(current, "applied", "applied-key", domain.MissionDeploymentApplied)); err != nil {
			t.Fatal(err)
		}
		intent, err := store.GetOperationalIntent(ctx, flight.IntentID)
		if err != nil {
			t.Fatal(err)
		}
		intent.Status = domain.IntentStatusComplete
		if err := store.UpdateOperationalIntent(ctx, intent, intent.Revision); err != nil {
			t.Fatal(err)
		}
		active := flight
		active.Status = domain.FlightStatusActive
		if err := store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned); !errors.Is(err, durable.ErrVersionConflict) {
			t.Fatalf("start error = %v", err)
		}
	})
}

func TestMissionImportAndStartRaceIsSerialized(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		store, flight, mission := missionLifecycleStore(t)
		ctx := context.Background()
		current, err := store.CreateMissionForPlannedFlight(ctx, mission)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, lifecycleDeployment(current, "applied", "applied-key", domain.MissionDeploymentApplied)); err != nil {
			t.Fatal(err)
		}
		candidate := mission
		candidate.ID = fmt.Sprintf("mission-race-%d", iteration)
		candidate.IdempotencyKey = fmt.Sprintf("mission-race-key-%d", iteration)
		candidate.IdempotencyRequest = fmt.Sprintf("mission-race-request-%d", iteration)
		active := flight
		active.Status = domain.FlightStatusActive
		var createErr, startErr error
		ready := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-ready
			_, createErr = store.CreateMissionForPlannedFlight(ctx, candidate)
		}()
		go func() {
			defer group.Done()
			<-ready
			startErr = store.StartFlightWithCurrentMissionDeployment(ctx, active, domain.FlightStatusPlanned)
		}()
		close(ready)
		group.Wait()
		if createErr == nil && startErr == nil {
			t.Fatal("mission import and flight start both committed")
		}
		if createErr != nil && !errors.Is(createErr, durable.ErrVersionConflict) {
			t.Fatalf("create error = %v", createErr)
		}
		if startErr != nil && !errors.Is(startErr, durable.ErrVersionConflict) {
			t.Fatalf("start error = %v", startErr)
		}
	}
}

func TestMissionDeploymentAndStartRaceIsSerialized(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		store, flight, mission := missionLifecycleStore(t)
		ctx := context.Background()
		current, err := store.CreateMissionForPlannedFlight(ctx, mission)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateMissionDeploymentForPlannedFlight(ctx, lifecycleDeployment(current, "applied", "applied-key", domain.MissionDeploymentApplied)); err != nil {
			t.Fatal(err)
		}
		candidate := lifecycleDeployment(
			current,
			fmt.Sprintf("deployment-race-%d", iteration),
			fmt.Sprintf("deployment-race-key-%d", iteration),
			domain.MissionDeploymentPending,
		)
		active := flight
		active.Status = domain.FlightStatusActive
		var createErr, startErr error
		ready := make(chan struct{})
		var group sync.WaitGroup
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
}

func missionLifecycleStore(t *testing.T) (*Store, domain.FlightRecord, domain.Mission) {
	t.Helper()
	store := NewStore()
	ctx := context.Background()
	if err := store.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", OperatorID: "operator-1", AgentID: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: "intent-1", Version: 1, OperatorID: "operator-1", AircraftID: "aircraft-1", Status: domain.IntentStatusActive}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{ID: "flight-1", OperatorID: "operator-1", AircraftID: "aircraft-1", IntentID: "intent-1", IntentVersion: 1, Status: domain.FlightStatusPlanned}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	mission := domain.Mission{ID: "mission-1", OperatorID: "operator-1", FlightID: flight.ID, AircraftID: flight.AircraftID, IntentID: flight.IntentID, IntentVersion: flight.IntentVersion, MissionDigest: "digest-1", IdempotencyKey: "mission-key", IdempotencyRequest: "mission-request"}
	return store, flight, mission
}

func lifecycleDeployment(mission domain.Mission, id, key string, status domain.MissionDeploymentStatus) domain.MissionDeployment {
	return domain.MissionDeployment{ID: id, FlightID: mission.FlightID, MissionID: mission.ID, MissionDigest: mission.MissionDigest, IdempotencyKey: key, IdempotencyRequest: key + "-request", Status: status}
}
