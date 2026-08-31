package preflight

import (
	"errors"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestAircraftCheckerMissingAircraftDoesNotEmitStatus(t *testing.T) {
	snapshot := testSnapshot(timeNow())
	snapshot.AircraftErr = errors.New("missing")
	builder := evaluateChecker(t, AircraftChecker{}, snapshot)
	requireCheck(t, builder, "aircraft_exists", "AIRCRAFT-EXISTS", "fleet_registry", true)
	requireNoCheck(t, builder, "aircraft_operational_status")
}

func TestAircraftCheckerBlocksWhenNotActiveAndAccepted(t *testing.T) {
	snapshot := testSnapshot(timeNow())
	snapshot.Aircraft = domain.Aircraft{Status: domain.AircraftStatusInactive, AcceptanceStatus: domain.AcceptanceStatusAccepted}
	builder := evaluateChecker(t, AircraftChecker{}, snapshot)
	requireCheck(t, builder, "aircraft_exists", "AIRCRAFT-EXISTS", "fleet_registry", false)
	requireCheck(t, builder, "aircraft_operational_status", "AIRCRAFT-STATUS", "fleet_registry", true)
}

func TestAircraftCheckerClearWhenActiveAndAccepted(t *testing.T) {
	snapshot := testSnapshot(timeNow())
	snapshot.Aircraft = domain.Aircraft{Status: domain.AircraftStatusActive, AcceptanceStatus: domain.AcceptanceStatusAccepted}
	builder := evaluateChecker(t, AircraftChecker{}, snapshot)
	requireCheck(t, builder, "aircraft_exists", "AIRCRAFT-EXISTS", "fleet_registry", false)
	requireCheck(t, builder, "aircraft_operational_status", "AIRCRAFT-STATUS", "fleet_registry", false)
}
