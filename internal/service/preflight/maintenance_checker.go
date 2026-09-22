package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type MaintenanceChecker struct {
	durable durable.Store
}

func (MaintenanceChecker) Name() string { return "maintenance" }

func (c MaintenanceChecker) Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder) {
	events, err := c.durable.ListMaintenanceEvents(ctx, snapshot.Intent.AircraftID)
	if err != nil {
		builder.Block(domain.PreflightCheckMaintenance, "maintenance_events_available", "maintenance_control", "MX-AVAILABLE", "maintenance status could not be loaded", "retry maintenance status lookup")
		return
	}
	for _, event := range events {
		if event.Severity == domain.SeverityCritical && event.ResolvedAt == nil && event.Status != domain.MaintenanceStatusClosed {
			builder.Block(domain.PreflightCheckMaintenance, "critical_open_maintenance", "maintenance_control", "MX-CRITICAL-OPEN", "critical open maintenance event exists", "resolve critical maintenance before activation")
			return
		}
	}
	builder.Clear(domain.PreflightCheckMaintenance, "critical_open_maintenance", "maintenance_control", "MX-CRITICAL-OPEN", "no critical open maintenance events")
}
