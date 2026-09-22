package preflight

import (
	"context"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestBatteryCheckerBlocksWhenNotInstalled(t *testing.T) {
	store := durablememory.NewStore()
	builder := evaluateChecker(t, BatteryChecker{durable: store}, testSnapshot(timeNow()))
	requireCheck(t, builder, "battery_installed", "BATTERY-INSTALLED", "maintenance_control", true)
	requireNoCheck(t, builder, "battery_soh_known")
}

func TestBatteryCheckerBlocksWhenSOHMissing(t *testing.T) {
	ctx := context.Background()
	now := timeNow()
	store := durablememory.NewStore()
	if err := store.CreateBattery(ctx, domain.Battery{ID: "battery-1", OperatorID: "operator-1", Status: domain.MaintenanceStatusCurrent, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBatteryInstallation(ctx, domain.BatteryInstallation{ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: now}); err != nil {
		t.Fatal(err)
	}
	builder := evaluateChecker(t, BatteryChecker{durable: store}, testSnapshot(now))
	requireCheck(t, builder, "battery_installed", "BATTERY-INSTALLED", "maintenance_control", false)
	requireCheck(t, builder, "battery_soh_known", "BATTERY-SOH-KNOWN", "maintenance_control", true)
	requireNoCheck(t, builder, "battery_soh_minimum")
}

func TestBatteryCheckerThresholdIsEighty(t *testing.T) {
	ctx := context.Background()
	now := timeNow()
	low := 79.0
	ok := 80.0
	for _, test := range []struct {
		soh     *float64
		blocked bool
	}{{soh: &low, blocked: true}, {soh: &ok, blocked: false}} {
		store := durablememory.NewStore()
		if err := store.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: test.soh, Status: domain.MaintenanceStatusCurrent, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordBatteryInstallation(ctx, domain.BatteryInstallation{ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: now}); err != nil {
			t.Fatal(err)
		}
		builder := evaluateChecker(t, BatteryChecker{durable: store}, testSnapshot(now))
		requireCheck(t, builder, "battery_soh_known", "BATTERY-SOH-KNOWN", "maintenance_control", false)
		requireCheck(t, builder, "battery_soh_minimum", "BATTERY-SOH-80", "maintenance_control", test.blocked)
	}
}
