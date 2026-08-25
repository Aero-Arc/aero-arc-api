package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type AircraftChecker struct{}

func (AircraftChecker) Name() string { return "aircraft" }

func (AircraftChecker) Evaluate(_ context.Context, snapshot Snapshot, builder *Builder) {
	if snapshot.AircraftErr != nil {
		builder.Block(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft does not exist", "create or select a valid aircraft")
		return
	}
	builder.Clear(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft exists")
	if snapshot.Aircraft.Status != domain.AircraftStatusActive || snapshot.Aircraft.AcceptanceStatus != domain.AcceptanceStatusAccepted {
		builder.Block(domain.PreflightCheckAirspace, "aircraft_operational_status", "fleet_registry", "AIRCRAFT-STATUS", "aircraft is not active or accepted", "set aircraft active or complete acceptance")
	} else {
		builder.Clear(domain.PreflightCheckAirspace, "aircraft_operational_status", "fleet_registry", "AIRCRAFT-STATUS", "aircraft status allows operation")
	}
}
