package relaycontrol

import (
	"context"
	"testing"
)

func TestGRPCPoolReusesConnectionOnlyWhileRelayAddressMatches(t *testing.T) {
	pool := newGRPCPool()
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close pool: %v", err)
		}
	})

	ctx := context.Background()
	if _, err := pool.Client(ctx, "relay-1", "127.0.0.1:50051"); err != nil {
		t.Fatalf("create first client: %v", err)
	}
	first := pool.conns["relay-1"]

	if _, err := pool.Client(ctx, "relay-1", "127.0.0.1:50051"); err != nil {
		t.Fatalf("reuse client: %v", err)
	}
	if reused := pool.conns["relay-1"]; reused.conn != first.conn {
		t.Fatal("same relay address replaced the pooled connection")
	}

	if _, err := pool.Client(ctx, "relay-1", "127.0.0.1:50052"); err != nil {
		t.Fatalf("replace client: %v", err)
	}
	replaced := pool.conns["relay-1"]
	if replaced.address != "127.0.0.1:50052" {
		t.Fatalf("pooled address = %q, want %q", replaced.address, "127.0.0.1:50052")
	}
	if replaced.conn == first.conn {
		t.Fatal("changed relay address reused the stale pooled connection")
	}
}
