package preflight

import (
	"errors"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestRemoteIDCheckerSkipsWhenAircraftMissing(t *testing.T) {
	snapshot := testSnapshot(timeNow())
	snapshot.AircraftErr = errors.New("missing")
	builder := evaluateChecker(t, RemoteIDChecker{}, snapshot)
	requireNoCheck(t, builder, "remote_id_online")
	if len(builder.Checks()) != 0 {
		t.Fatalf("checks = %#v, want none", builder.Checks())
	}
}

func TestRemoteIDCheckerBlocksWhenOffline(t *testing.T) {
	snapshot := testSnapshot(timeNow())
	snapshot.Aircraft.RemoteIDStatus = domain.RemoteIDStatusOffline
	builder := evaluateChecker(t, RemoteIDChecker{}, snapshot)
	check := requireCheck(t, builder, "remote_id_online", "RID-ONLINE", "remote_id_monitor", true)
	if check.Summary != "remote ID is offline" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestRemoteIDCheckerClearsWhenNotOffline(t *testing.T) {
	snapshot := testSnapshot(timeNow())
	snapshot.Aircraft.RemoteIDStatus = domain.RemoteIDStatusBroadcasting
	builder := evaluateChecker(t, RemoteIDChecker{}, snapshot)
	requireCheck(t, builder, "remote_id_online", "RID-ONLINE", "remote_id_monitor", false)
}
