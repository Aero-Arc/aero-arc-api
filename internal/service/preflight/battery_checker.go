package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type BatteryChecker struct {
	durable durable.Store
}

func (BatteryChecker) Name() string { return "battery" }

func (c BatteryChecker) Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder) {
	installation, err := c.durable.GetActiveBatteryInstallation(ctx, snapshot.Intent.AircraftID)
	if err != nil || installation == nil {
		builder.Block(domain.PreflightCheckBattery, "battery_installed", "maintenance_control", "BATTERY-INSTALLED", "battery is not installed", "install a battery")
		return
	}
	builder.Clear(domain.PreflightCheckBattery, "battery_installed", "maintenance_control", "BATTERY-INSTALLED", "battery is installed")

	battery, err := c.durable.GetBattery(ctx, installation.BatteryID)
	if err != nil {
		builder.Block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")
		return
	}
	if battery.StateOfHealth == nil {
		builder.Block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")
		return
	}
	builder.Clear(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is known")
	if *battery.StateOfHealth < 80 {
		builder.Block(domain.PreflightCheckBattery, "battery_soh_minimum", "maintenance_control", "BATTERY-SOH-80", "battery state of health is below 80", "replace or service battery")
		return
	}
	builder.Clear(domain.PreflightCheckBattery, "battery_soh_minimum", "maintenance_control", "BATTERY-SOH-80", "battery state of health is at least 80")
}
