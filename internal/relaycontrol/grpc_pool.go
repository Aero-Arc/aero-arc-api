package relaycontrol

import (
	"context"
	"sync"

	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// grpcPool owns and reuses the API's connections to relay instances discovered
// through the Registry service.
type grpcPool struct {
	mu                   sync.Mutex
	transportCredentials credentials.TransportCredentials
	conns                map[string]pooledConnection
}

type pooledConnection struct {
	address string
	conn    *grpc.ClientConn
}

func newGRPCPool(transportCredentials credentials.TransportCredentials) *grpcPool {
	return &grpcPool{
		transportCredentials: transportCredentials,
		conns:                map[string]pooledConnection{},
	}
}

func (p *grpcPool) Client(ctx context.Context, relayID, address string) (relayv1.RelayControlClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.conns[relayID]; ok {
		if existing.address == address {
			return relayv1.NewRelayControlClient(existing.conn), nil
		}
		_ = existing.conn.Close()
		delete(p.conns, relayID)
	}
	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(p.transportCredentials))
	if err != nil {
		return nil, err
	}
	p.conns[relayID] = pooledConnection{address: address, conn: conn}
	return relayv1.NewRelayControlClient(conn), nil
}

func (p *grpcPool) Invalidate(relayID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.conns[relayID]; ok {
		_ = existing.conn.Close()
		delete(p.conns, relayID)
	}
}

func (p *grpcPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var first error
	for id, existing := range p.conns {
		if err := existing.conn.Close(); err != nil && first == nil {
			first = err
		}
		delete(p.conns, id)
	}
	return first
}
