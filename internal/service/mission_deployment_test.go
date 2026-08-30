package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
)

type fakeMissionDeployer struct {
	contextErr error
	deployErr  error
	deployHook func()
	contexts   []*agentv1.SetOperationContextCommand
	commands   []*agentv1.DeployMissionCommand
	agentIDs   []string
}

func (f *fakeMissionDeployer) EnsureOperationContext(_ context.Context, agentID string, command *agentv1.SetOperationContextCommand) error {
	f.agentIDs = append(f.agentIDs, agentID)
	f.contexts = append(f.contexts, command)
	return f.contextErr
}

func (f *fakeMissionDeployer) ClearOperationContextForReconciliation(_ context.Context, _ string, _ *agentv1.ClearOperationContextCommand, _ *agentv1.OperationContext) error {
	return nil
}

func (f *fakeMissionDeployer) DeployMission(_ context.Context, agentID string, command *agentv1.DeployMissionCommand) (*agentv1.MissionDeploymentResult, error) {
	f.agentIDs = append(f.agentIDs, agentID)
	f.commands = append(f.commands, command)
	if f.deployHook != nil {
		f.deployHook()
	}
	if f.deployErr != nil {
		return nil, f.deployErr
	}
	return &agentv1.MissionDeploymentResult{
		CommandId: command.GetCommandId(), Binding: command.GetBinding(),
		Status:               agentv1.MissionDeploymentResult_STATUS_APPLIED,
		UploadedItemCount:    uint32(len(command.GetPlan().GetItems())),
		OnboardMissionDigest: command.GetBinding().GetMissionDigest(),
		CompletedAtUnixMs:    1,
	}, nil
}

func TestDeployCurrentMissionEnsuresContextThenDispatchesExactMission(t *testing.T) {
	svc, store := newMissionTestService(t)
	mission, err := svc.ImportMission(context.Background(), "flight-1", "import-for-deploy", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{}
	svc.WithMissionDeployer(deployer)
	result, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-key")
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed || result.Deployment.Status != domain.MissionDeploymentApplied || result.Deployment.AttemptCount != 1 {
		t.Fatalf("deployment = %#v", result)
	}
	if len(deployer.contexts) != 1 || len(deployer.commands) != 1 || len(deployer.agentIDs) != 2 || deployer.agentIDs[0] != "agent-1" || deployer.agentIDs[1] != "agent-1" {
		t.Fatalf("calls = context:%d deploy:%d agents:%v", len(deployer.contexts), len(deployer.commands), deployer.agentIDs)
	}
	operation := deployer.contexts[0].GetContext()
	if operation.GetAircraftId() != "aircraft-1" || operation.GetFlightId() != "flight-1" || operation.GetIntentId() != "intent-1" || operation.GetIntentVersion() != 2 {
		t.Fatalf("operation context = %#v", operation)
	}
	command := deployer.commands[0]
	if command.GetBinding().GetMissionId() != mission.Mission.ID || command.GetBinding().GetDeploymentId() != result.Deployment.ID ||
		command.GetBinding().GetMissionDigest() != mission.Mission.MissionDigest || len(command.GetPlan().GetItems()) != len(mission.Mission.Items) {
		t.Fatalf("deploy command = %#v", command)
	}
	replay, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-key")
	if err != nil || !replay.Replayed || replay.Deployment.ID != result.Deployment.ID || len(deployer.commands) != 1 {
		t.Fatalf("terminal replay = %#v err=%v calls=%d", replay, err, len(deployer.commands))
	}
	flight, err := store.GetFlightRecord(context.Background(), "flight-1")
	if err != nil {
		t.Fatal(err)
	}
	flight.Status = domain.FlightStatusActive
	if err := store.UpdateFlightRecord(context.Background(), flight, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	postTransitionReplay, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-key")
	if err != nil || !postTransitionReplay.Replayed || postTransitionReplay.Deployment.ID != result.Deployment.ID || len(deployer.commands) != 1 {
		t.Fatalf("post-transition terminal replay = %#v err=%v calls=%d", postTransitionReplay, err, len(deployer.commands))
	}
}

func TestDeployCurrentMissionDoesNotDispatchWithoutContextAck(t *testing.T) {
	svc, _ := newMissionTestService(t)
	mission, err := svc.ImportMission(context.Background(), "flight-1", "import-context-failure", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{contextErr: errors.New("agent offline")}
	svc.WithMissionDeployer(deployer)
	first, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-context-failure")
	if err != nil {
		t.Fatal(err)
	}
	if first.Deployment.Status != domain.MissionDeploymentTemporaryError || len(deployer.commands) != 0 || len(deployer.contexts) != 1 {
		t.Fatalf("first = %#v calls=%d/%d", first, len(deployer.contexts), len(deployer.commands))
	}
	contextCommandID := deployer.contexts[0].GetCommandId()
	deployer.contextErr = nil
	retry, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-context-failure")
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Replayed || retry.Deployment.Status != domain.MissionDeploymentApplied || len(deployer.commands) != 1 ||
		deployer.contexts[1].GetCommandId() != contextCommandID || deployer.commands[0].GetCommandId() != first.Deployment.CommandID {
		t.Fatalf("retry = %#v contexts=%#v commands=%#v", retry, deployer.contexts, deployer.commands)
	}
}

func TestDeployCurrentMissionRejectsConflictingIdempotencyAndLifecycle(t *testing.T) {
	svc, store := newMissionTestService(t)
	firstMission, err := svc.ImportMission(context.Background(), "flight-1", "first-import", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{}
	svc.WithMissionDeployer(deployer)
	firstDeployment, err := svc.DeployCurrentMission(context.Background(), "flight-1", firstMission.Mission.ID, firstMission.Mission.MissionDigest, "stable-deploy-key")
	if err != nil {
		t.Fatal(err)
	}
	changed := validMissionRequest(validWPL110)
	changed.Source = "QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t-35.3632620\t149.1652370\t0\t1\n1\t0\t0\t22\t0\t0\t0\t0\t-35.3632620\t149.1652370\t20\t1\n2\t0\t0\t21\t0\t0\t0\t1\t-35.3632620\t149.1652370\t0\t1\n"
	secondMission, err := svc.ImportMission(context.Background(), "flight-1", "second-import", changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", secondMission.Mission.ID, secondMission.Mission.MissionDigest, "stable-deploy-key"); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("conflicting deployment error = %v", err)
	}
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", firstMission.Mission.ID, firstMission.Mission.MissionDigest, "stable-deploy-key"); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("superseded original deployment replay error = %v", err)
	}
	if _, err := svc.ReconcileMissionDeployment(context.Background(), "flight-1", firstDeployment.Deployment.ID); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("superseded terminal deployment reconciliation error = %v", err)
	}
	flight, err := store.GetFlightRecord(context.Background(), "flight-1")
	if err != nil {
		t.Fatal(err)
	}
	flight.Status = domain.FlightStatusActive
	if err := store.UpdateFlightRecord(context.Background(), flight, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", secondMission.Mission.ID, secondMission.Mission.MissionDigest, "active-deploy"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("active flight deployment error = %v", err)
	}
}

func TestDeployCurrentMissionRejectsReviewedMissionAfterRouteReplacement(t *testing.T) {
	svc, _ := newMissionTestService(t)
	reviewed, err := svc.ImportMission(context.Background(), "flight-1", "reviewed-import", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	changed := validMissionRequest(validWPL110)
	changed.Source = "QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t-35.3632620\t149.1652370\t0\t1\n1\t0\t0\t22\t0\t0\t0\t0\t-35.3632620\t149.1652370\t20\t1\n2\t0\t0\t21\t0\t0\t0\t1\t-35.3632620\t149.1652370\t0\t1\n"
	if _, err := svc.ImportMission(context.Background(), "flight-1", "replacement-import", changed); err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{}
	svc.WithMissionDeployer(deployer)
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", reviewed.Mission.ID, reviewed.Mission.MissionDigest, "reviewed-deploy"); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale reviewed mission error = %v", err)
	}
	if len(deployer.contexts) != 0 || len(deployer.commands) != 0 {
		t.Fatalf("stale mission reached control plane: contexts=%d commands=%d", len(deployer.contexts), len(deployer.commands))
	}
}

func TestDeployCurrentMissionRetainsTransportUnknown(t *testing.T) {
	svc, _ := newMissionTestService(t)
	mission, err := svc.ImportMission(context.Background(), "flight-1", "import-unknown", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{deployErr: context.DeadlineExceeded}
	svc.WithMissionDeployer(deployer)
	result, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if result.Deployment.Status != domain.MissionDeploymentOutcomeUnknown || result.Deployment.Message == "" || len(deployer.commands) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "another-deploy-key"); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("second unresolved deployment error = %v, want version conflict", err)
	}
}

func TestDeployCurrentMissionPersistsUnknownBeforeRelayCall(t *testing.T) {
	svc, store := newMissionTestService(t)
	mission, err := svc.ImportMission(context.Background(), "flight-1", "import-dispatch-marker", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{}
	deployer.deployHook = func() {
		persisted, getErr := store.GetMissionDeploymentByIdempotencyKey(context.Background(), "dispatch-marker")
		if getErr != nil {
			t.Fatal(getErr)
		}
		if persisted.Status != domain.MissionDeploymentOutcomeUnknown || persisted.AttemptCount != 1 || persisted.Revision != 1 {
			t.Fatalf("pre-dispatch marker = %#v", persisted)
		}
	}
	svc.WithMissionDeployer(deployer)
	result, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "dispatch-marker")
	if err != nil {
		t.Fatal(err)
	}
	if result.Deployment.Status != domain.MissionDeploymentApplied || result.Deployment.AttemptCount != 1 || result.Deployment.Revision != 2 {
		t.Fatalf("deployment = %#v", result.Deployment)
	}
}

func TestMissionDeploymentAlreadyAppliedAcceptsReadbackOnlyZeroUploadCount(t *testing.T) {
	deployment := domain.MissionDeployment{CommandID: "command-1", MissionDigest: "digest-1"}
	binding := &agentv1.MissionBinding{MissionId: "mission-1", MissionVersion: 1, MissionDigest: "digest-1", DeploymentId: "deployment-1", OperatorId: "operator-1", AircraftId: "aircraft-1", FlightId: "flight-1", IntentId: "intent-1", IntentVersion: 1}
	result := &agentv1.MissionDeploymentResult{
		CommandId: "command-1", Binding: binding, Status: agentv1.MissionDeploymentResult_STATUS_ALREADY_APPLIED,
		UploadedItemCount: 0, OnboardMissionDigest: "digest-1",
	}
	if err := applyMissionDeploymentResult(&deployment, result, binding, 3); err != nil {
		t.Fatal(err)
	}
	if deployment.Status != domain.MissionDeploymentAlreadyApplied || deployment.UploadedItemCount != 0 {
		t.Fatalf("deployment = %#v", deployment)
	}
	result.Status = agentv1.MissionDeploymentResult_STATUS_APPLIED
	if err := applyMissionDeploymentResult(&deployment, result, binding, 3); err != nil {
		t.Fatal(err)
	}
	if deployment.Status != domain.MissionDeploymentOnboardMissionMismatch {
		t.Fatalf("fresh APPLIED with zero count status = %q", deployment.Status)
	}
}

func TestGetMissionDeploymentScopesDurableResultToFlight(t *testing.T) {
	svc, _ := newMissionTestService(t)
	mission, err := svc.ImportMission(context.Background(), "flight-1", "import-for-status", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	svc.WithMissionDeployer(&fakeMissionDeployer{})
	deployed, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "deploy-for-status")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetMissionDeployment(context.Background(), "flight-1", deployed.Deployment.ID)
	if err != nil || got.ID != deployed.Deployment.ID {
		t.Fatalf("deployment = %#v err=%v", got, err)
	}
	if _, err := svc.GetMissionDeployment(context.Background(), "another-flight", deployed.Deployment.ID); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("cross-flight deployment error = %v", err)
	}
	if _, err := svc.GetMissionDeployment(context.Background(), "flight-1", "missing-deployment"); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("missing deployment error = %v", err)
	}
	if _, err := svc.GetMissionDeployment(context.Background(), "", deployed.Deployment.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid scope error = %v", err)
	}
}

func TestMissionDeploymentRestoreAndReconcileReusesDurableCommand(t *testing.T) {
	svc, store := newMissionTestService(t)
	mission, err := svc.ImportMission(context.Background(), "flight-1", "import-for-restore", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{deployErr: context.DeadlineExceeded}
	svc.WithMissionDeployer(deployer)
	first, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "restore-deploy-key")
	if err != nil {
		t.Fatal(err)
	}
	if first.Deployment.Status != domain.MissionDeploymentOutcomeUnknown {
		t.Fatalf("first deployment = %#v", first.Deployment)
	}
	terminal := first.Deployment
	terminal.ID = "newer-terminal-deployment"
	terminal.IdempotencyKey = "newer-terminal-key"
	terminal.IdempotencyRequest = strings.Repeat("e", 64)
	terminal.CommandID = "newer-terminal-command"
	terminal.Status = domain.MissionDeploymentRejected
	terminal.CreatedAt = first.Deployment.CreatedAt.Add(time.Second)
	if _, err := store.CreateMissionDeployment(context.Background(), terminal); err != nil {
		t.Fatal(err)
	}
	restored, err := svc.GetCurrentMissionDeployment(context.Background(), "flight-1")
	if err != nil || restored.ID != first.Deployment.ID {
		t.Fatalf("restored deployment = %#v err=%v, want outstanding %s", restored, err, first.Deployment.ID)
	}

	deployer.deployErr = nil
	reconciled, err := svc.ReconcileMissionDeployment(context.Background(), "flight-1", restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reconciled.Replayed || reconciled.Deployment.ID != first.Deployment.ID || reconciled.Deployment.Status != domain.MissionDeploymentApplied {
		t.Fatalf("reconciled deployment = %#v", reconciled)
	}
	if len(deployer.commands) != 2 || deployer.commands[0].GetCommandId() != deployer.commands[1].GetCommandId() ||
		deployer.commands[1].GetCommandId() != first.Deployment.CommandID {
		t.Fatalf("reconcile command IDs = %#v", deployer.commands)
	}
	if _, err := svc.ReconcileMissionDeployment(context.Background(), "another-flight", restored.ID); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("cross-flight reconcile error = %v", err)
	}
	terminalReplay, err := svc.ReconcileMissionDeployment(context.Background(), "flight-1", restored.ID)
	if err != nil || terminalReplay.Deployment.ID != restored.ID || len(deployer.commands) != 2 {
		t.Fatalf("terminal reconcile = %#v err=%v calls=%d", terminalReplay, err, len(deployer.commands))
	}
}

func TestMissionDeploymentReconciliationWindowFencesPostExpiryEffects(t *testing.T) {
	for _, test := range []struct {
		name        string
		retryAt     func(domain.MissionDeployment) time.Time
		wantCalls   int
		wantStatus  domain.MissionDeploymentStatus
		wantMessage string
	}{
		{
			name: "uncertain command retries just before reconciliation deadline",
			retryAt: func(deployment domain.MissionDeployment) time.Time {
				return deployment.ReconcileUntil.Add(-time.Nanosecond)
			},
			wantCalls:  2,
			wantStatus: domain.MissionDeploymentApplied,
		},
		{
			name:        "reconciliation closes exactly at deadline",
			retryAt:     func(deployment domain.MissionDeployment) time.Time { return deployment.ReconcileUntil },
			wantCalls:   1,
			wantStatus:  domain.MissionDeploymentOutcomeUnknown,
			wantMessage: "deployment reconciliation window elapsed with no correlated terminal outcome",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := newMissionTestService(t)
			mission, err := svc.ImportMission(context.Background(), "flight-1", "window-import", validMissionRequest(validWPL110))
			if err != nil {
				t.Fatal(err)
			}
			deployer := &fakeMissionDeployer{deployErr: context.DeadlineExceeded}
			svc.WithMissionDeployer(deployer)
			first, err := svc.DeployCurrentMission(context.Background(), "flight-1", mission.Mission.ID, mission.Mission.MissionDigest, "window-deploy")
			if err != nil {
				t.Fatal(err)
			}
			clock := test.retryAt(first.Deployment)
			svc.now = func() time.Time { return clock }
			deployer.deployErr = nil
			reconciled, err := svc.ReconcileMissionDeployment(context.Background(), "flight-1", first.Deployment.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(deployer.commands) != test.wantCalls || reconciled.Deployment.Status != test.wantStatus ||
				(test.wantMessage != "" && reconciled.Deployment.Message != test.wantMessage) {
				t.Fatalf("reconciled = %#v calls=%d", reconciled.Deployment, len(deployer.commands))
			}
			if len(deployer.commands) == 2 && deployer.commands[0].GetCommandId() != deployer.commands[1].GetCommandId() {
				t.Fatalf("post-expiry reconcile changed command ID: %#v", deployer.commands)
			}
		})
	}
}
