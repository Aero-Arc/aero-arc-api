package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
)

type fakeMissionDeployer struct {
	contextErr error
	deployErr  error
	contexts   []*agentv1.SetOperationContextCommand
	commands   []*agentv1.DeployMissionCommand
	agentIDs   []string
}

func (f *fakeMissionDeployer) EnsureOperationContext(_ context.Context, agentID string, command *agentv1.SetOperationContextCommand) error {
	f.agentIDs = append(f.agentIDs, agentID)
	f.contexts = append(f.contexts, command)
	return f.contextErr
}

func (f *fakeMissionDeployer) DeployMission(_ context.Context, agentID string, command *agentv1.DeployMissionCommand) (*agentv1.MissionDeploymentResult, error) {
	f.agentIDs = append(f.agentIDs, agentID)
	f.commands = append(f.commands, command)
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
	result, err := svc.DeployCurrentMission(context.Background(), "flight-1", "deploy-key")
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
	replay, err := svc.DeployCurrentMission(context.Background(), "flight-1", "deploy-key")
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
	postTransitionReplay, err := svc.DeployCurrentMission(context.Background(), "flight-1", "deploy-key")
	if err != nil || !postTransitionReplay.Replayed || postTransitionReplay.Deployment.ID != result.Deployment.ID || len(deployer.commands) != 1 {
		t.Fatalf("post-transition terminal replay = %#v err=%v calls=%d", postTransitionReplay, err, len(deployer.commands))
	}
}

func TestDeployCurrentMissionDoesNotDispatchWithoutContextAck(t *testing.T) {
	svc, _ := newMissionTestService(t)
	if _, err := svc.ImportMission(context.Background(), "flight-1", "import-context-failure", validMissionRequest(validWPL110)); err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{contextErr: errors.New("agent offline")}
	svc.WithMissionDeployer(deployer)
	first, err := svc.DeployCurrentMission(context.Background(), "flight-1", "deploy-context-failure")
	if err != nil {
		t.Fatal(err)
	}
	if first.Deployment.Status != domain.MissionDeploymentTemporaryError || len(deployer.commands) != 0 || len(deployer.contexts) != 1 {
		t.Fatalf("first = %#v calls=%d/%d", first, len(deployer.contexts), len(deployer.commands))
	}
	contextCommandID := deployer.contexts[0].GetCommandId()
	deployer.contextErr = nil
	retry, err := svc.DeployCurrentMission(context.Background(), "flight-1", "deploy-context-failure")
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
	if _, err := svc.ImportMission(context.Background(), "flight-1", "first-import", validMissionRequest(validWPL110)); err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{}
	svc.WithMissionDeployer(deployer)
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", "stable-deploy-key"); err != nil {
		t.Fatal(err)
	}
	changed := validMissionRequest(validWPL110)
	changed.Source = "QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t-35.3632620\t149.1652370\t0\t1\n1\t0\t0\t22\t0\t0\t0\t0\t-35.3632620\t149.1652370\t20\t1\n2\t0\t0\t21\t0\t0\t0\t1\t-35.3632620\t149.1652370\t0\t1\n"
	if _, err := svc.ImportMission(context.Background(), "flight-1", "second-import", changed); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", "stable-deploy-key"); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("conflicting deployment error = %v", err)
	}
	flight, err := store.GetFlightRecord(context.Background(), "flight-1")
	if err != nil {
		t.Fatal(err)
	}
	flight.Status = domain.FlightStatusActive
	if err := store.UpdateFlightRecord(context.Background(), flight, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeployCurrentMission(context.Background(), "flight-1", "active-deploy"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("active flight deployment error = %v", err)
	}
}

func TestDeployCurrentMissionRetainsTransportUnknown(t *testing.T) {
	svc, _ := newMissionTestService(t)
	if _, err := svc.ImportMission(context.Background(), "flight-1", "import-unknown", validMissionRequest(validWPL110)); err != nil {
		t.Fatal(err)
	}
	deployer := &fakeMissionDeployer{deployErr: context.DeadlineExceeded}
	svc.WithMissionDeployer(deployer)
	result, err := svc.DeployCurrentMission(context.Background(), "flight-1", "deploy-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if result.Deployment.Status != domain.MissionDeploymentOutcomeUnknown || result.Deployment.Message == "" || len(deployer.commands) != 1 {
		t.Fatalf("result = %#v", result)
	}
}
