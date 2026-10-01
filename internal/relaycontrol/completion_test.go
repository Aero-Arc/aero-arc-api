package relaycontrol

import (
	"context"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc"
	"testing"
	"time"
)

type rotatingCompletionPool struct {
	clientPool
	reached []string
}

func (p *rotatingCompletionPool) Client(ctx context.Context, id, _ string) (relayv1.RelayControlClient, error) {
	p.reached = append(p.reached, id)
	if id == "relay-1" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &emptyCompletionClient{}, nil
}

type emptyCompletionClient struct{ relayv1.RelayControlClient }

func (*emptyCompletionClient) ListFlightCompletions(context.Context, *relayv1.ListFlightCompletionsRequest, ...grpc.CallOption) (*relayv1.ListFlightCompletionsResponse, error) {
	return &relayv1.ListFlightCompletionsResponse{}, nil
}
func TestCompletionPollingRotatesPastUnreachableRelay(t *testing.T) {
	p := &rotatingCompletionPool{}
	s := newWithPool(&fakeRegistry{}, p, time.Second, time.Second)
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_ = s.DrainFlightCompletions(ctx, func(context.Context, *pb.FlightCompletionEvidence) error { t.Fatal("unexpected event"); return nil })
		cancel()
	}
	if len(p.reached) < 2 || p.reached[0] != "relay-1" || p.reached[1] != "relay-2" {
		t.Fatalf("later relay starved: %v", p.reached)
	}
}
