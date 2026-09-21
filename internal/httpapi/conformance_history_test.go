package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type historyRPC struct {
	conformancev1.UnimplementedConformanceServiceServer
	query      *conformancev1.ListConformanceEventsRequest
	err        error
	empty      bool
	wrongScope bool
	calls      int
}

func (s *historyRPC) ListConformanceEvents(_ context.Context, q *conformancev1.ListConformanceEventsRequest) (*conformancev1.ListConformanceEventsResponse, error) {
	s.query = q
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if s.empty {
		return &conformancev1.ListConformanceEventsResponse{}, nil
	}
	scope := q.AssignmentId
	if s.wrongScope {
		scope = "other"
	}
	zero := 0.0
	return &conformancev1.ListConformanceEventsResponse{Events: []*conformancev1.ConformanceHistoryEvent{{EventId: "event", AssignmentId: scope, IntentId: "intent", AircraftId: "aircraft", AssignmentGeneration: 2, Transition: "resolved", ViolationType: conformancev1.ViolationType_VIOLATION_TYPE_LATERAL_DEVIATION, ObservedAt: timestamppb.New(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)), DeviationM: &zero, PlannedStartAt: timestamppb.New(time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)), PlannedEndAt: timestamppb.New(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))}}, NextPageToken: "next"}, nil
}

func TestHistoryHTTPThroughGRPC(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	rpc := grpc.NewServer()
	backend := &historyRPC{}
	conformancev1.RegisterConformanceServiceServer(rpc, backend)
	serveResult := make(chan error, 1)
	go func() { serveResult <- rpc.Serve(listener) }()
	t.Cleanup(func() {
		rpc.Stop()
		if err := <-serveResult; err != nil {
			t.Errorf("serve history RPC: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///history", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close history client: %v", err)
		}
	})
	durable := durablememory.NewStore()
	if err := durable.CreateOperationalIntent(context.Background(), domain.OperationalIntent{ID: "intent", AircraftID: "aircraft"}); err != nil {
		t.Fatal(err)
	}
	fleet := service.NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), nil).WithConformanceHistory(conformancev1.NewConformanceServiceClient(conn))
	server := New(fleet, time.Second)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRecorder()
		server.Handler().ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		return r
	}
	path := "/api/v1/operational-intents/intent/conformance/events"
	r := get(path + "?generation=2&page_size=10&page_token=opaque&from=2026-09-01T00:00:00Z&until=2026-09-02T00:00:00Z")
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	var page service.ConformanceHistoryPage
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].DeviationM == nil || *page.Events[0].DeviationM != 0 || backend.query.AssignmentId != "intent" || backend.query.AssignmentGeneration != 2 || backend.query.PageSize != 10 || backend.query.PageToken != "opaque" || page.NextPageToken != "next" {
		t.Fatalf("page=%+v request=%+v", page, backend.query)
	}
	if page.Events[0].PlannedStartAt == nil || page.Events[0].PlannedEndAt == nil || !page.Events[0].PlannedEndAt.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("historical plan timestamps were not forwarded")
	}
	for _, query := range []string{"?generation=-1", "?generation=18446744073709551615", "?page_size=201", "?page_size=0", "?from=nope", "?from=2026-09-02T00:00:00Z&until=2026-09-01T00:00:00Z"} {
		if r := get(path + query); r.Code != 400 {
			t.Fatalf("query %s: %d", query, r.Code)
		}
	}
	calls := backend.calls
	if r := get("/api/v1/operational-intents/missing/conformance/events"); r.Code != 404 || backend.calls != calls {
		t.Fatalf("unknown intent: %d calls=%d", r.Code, backend.calls)
	}
	backend.empty = true
	if r := get(path); r.Code != 200 || r.Body.String() != "{\"events\":[]}\n" {
		t.Fatalf("empty page: %d %s", r.Code, r.Body)
	}
	backend.empty = false
	backend.wrongScope = true
	if r := get(path); r.Code != 503 {
		t.Fatalf("cross-scope reply: %d", r.Code)
	}
	backend.wrongScope = false
	for _, tc := range []struct {
		code codes.Code
		want int
	}{{codes.InvalidArgument, 400}, {codes.Unavailable, 503}, {codes.Unimplemented, 503}, {codes.DeadlineExceeded, 503}} {
		backend.err = status.Error(tc.code, "test")
		if r := get(path); r.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.code, r.Code, r.Body)
		}
	}
	fleet.WithConformanceHistory(nil)
	if r := get(path); r.Code != 503 {
		t.Fatalf("not configured: %d", r.Code)
	}
}
