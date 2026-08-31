package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type RemoteIDChecker struct{}

func (RemoteIDChecker) Name() string { return "remote_id" }

func (RemoteIDChecker) Evaluate(_ context.Context, snapshot Snapshot, builder *Builder) {
	if snapshot.AircraftErr != nil {
		return
	}
	if snapshot.Aircraft.RemoteIDStatus == domain.RemoteIDStatusOffline {
		builder.Block(domain.PreflightCheckRemoteID, "remote_id_online", "remote_id_monitor", "RID-ONLINE", "remote ID is offline", "restore Remote ID broadcast before activation")
	} else {
		builder.Clear(domain.PreflightCheckRemoteID, "remote_id_online", "remote_id_monitor", "RID-ONLINE", "remote ID is not offline")
	}
}
