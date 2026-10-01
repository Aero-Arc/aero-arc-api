package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
)

type reviewCommandStore struct {
	durable.Store
	durable.CommandStore
	requeued string
}

func (s *reviewCommandStore) GetFlightRecord(context.Context, string) (domain.FlightRecord, error) {
	return domain.FlightRecord{ID: "flight", OperatorID: "operator"}, nil
}
func (s *reviewCommandStore) GetMissionDeployment(context.Context, string) (domain.MissionDeployment, error) {
	return domain.MissionDeployment{ID: "deployment", FlightID: "flight", CommandID: "original-command", Status: domain.MissionDeploymentOutcomeUnknown}, nil
}
func (s *reviewCommandStore) GetCommand(context.Context, string) (domain.Command, error) {
	return domain.Command{ID: "original-command", FlightID: "flight", OperatorID: "operator"}, nil
}
func (s *reviewCommandStore) RequeueCommand(_ context.Context, id string) error {
	s.requeued = id
	return nil
}

type reviewCommandTransport struct{}

func (reviewCommandTransport) ExchangeCommand(context.Context, string, *pb.DurableCommand, string) (*pb.CommandEvidence, error) {
	panic("HTTP must not dispatch commands")
}

func TestReplayRemainsProtectedWithoutCommandTransport(t *testing.T) {
	for _, configured := range []bool{false, true} {
		store := &reviewCommandStore{}
		fleet := service.NewFleetService(store, nil, nil, nil)
		server := New(fleet, time.Second)
		want := http.StatusServiceUnavailable
		if configured {
			server.WithMissionDeploymentControl(time.Second, "secret")
			want = http.StatusUnauthorized
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/flights/flight/replay", nil))
		if response.Code != want {
			t.Fatalf("configured=%v: status=%d body=%s", configured, response.Code, response.Body.String())
		}
	}
}
func TestDeploymentReconcileRequeuesOriginalDurableCommand(t *testing.T) {
	store := &reviewCommandStore{}
	var actions []string
	fleet := service.NewFleetService(store, nil, nil, nil).WithCommandControl(reviewCommandTransport{}, func(_ context.Context, principal string, f domain.FlightRecord, action string) error {
		if principal != "mission-control-service" || f.ID != "flight" {
			t.Fatal("incorrect authorization binding")
		}
		actions = append(actions, action)
		return nil
	})
	handler := New(fleet, time.Second).WithMissionDeploymentControl(time.Second, "secret").Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight/mission-deployments/deployment/reconcile", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || store.requeued != "original-command" {
		t.Fatalf("status=%d body=%s requeued=%q", response.Code, response.Body.String(), store.requeued)
	}
	if len(actions) != 2 || actions[0] != "READ" || actions[1] != "RECONCILE" {
		t.Fatalf("authorization actions=%v", actions)
	}
}

func TestReplayHonorsFlightCommandReadPolicy(t *testing.T) {
	store := &reviewCommandStore{}
	called := false
	fleet := service.NewFleetService(store, nil, nil, nil).WithCommandControl(nil, func(_ context.Context, principal string, f domain.FlightRecord, action string) error {
		called = true
		if principal != "mission-control-service" || f.ID != "flight" || action != "READ" {
			t.Fatal("wrong replay policy binding")
		}
		return service.ErrValidation
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/flights/flight/replay", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	New(fleet, time.Second).WithMissionDeploymentControl(time.Second, "secret").Handler().ServeHTTP(response, request)
	if !called || response.Code < 400 {
		t.Fatalf("replay bypassed read policy: %d", response.Code)
	}
}
