package preflight

import (
	"context"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestMaintenanceCheckerClearsWhenNoCriticalOpen(t *testing.T) {
	builder := evaluateChecker(t, MaintenanceChecker{durable: durablememory.NewStore()}, testSnapshot(timeNow()))
	requireCheck(t, builder, "critical_open_maintenance", "MX-CRITICAL-OPEN", "maintenance_control", false)
	requireNoCheck(t, builder, "maintenance_events_available")
}

func TestMaintenanceCheckerBlocksCriticalOpen(t *testing.T) {
	ctx := context.Background()
	now := timeNow()
	store := durablememory.NewStore()
	if err := store.RecordMaintenanceEvent(ctx, domain.MaintenanceEvent{
		ID:         "mx-critical",
		AircraftID: "aircraft-1",
		Severity:   domain.SeverityCritical,
		Status:     domain.MaintenanceStatusOpen,
		Title:      "critical item",
		OpenedAt:   now,
	}); err != nil {
		t.Fatal(err)
	}
	builder := evaluateChecker(t, MaintenanceChecker{durable: store}, testSnapshot(now))
	requireCheck(t, builder, "critical_open_maintenance", "MX-CRITICAL-OPEN", "maintenance_control", true)
	requireNoCheck(t, builder, "maintenance_events_available")
}
