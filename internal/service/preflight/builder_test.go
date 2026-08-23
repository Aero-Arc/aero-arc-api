package preflight

import (
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func testSnapshot(now time.Time) Snapshot {
	return Snapshot{
		Intent: domain.OperationalIntent{
			ID:         "intent-1",
			Version:    2,
			OperatorID: "operator-1",
			AircraftID: "aircraft-1",
		},
		Now: now,
	}
}

func TestBuilderClearWritesPassFinding(t *testing.T) {
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	builder := newBuilder(testSnapshot(now))
	builder.Clear(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft exists")

	if builder.Blocked() {
		t.Fatal("clear should not block")
	}
	if len(builder.Checks()) != 1 || len(builder.Findings()) != 1 {
		t.Fatalf("checks=%d findings=%d, want 1 each", len(builder.Checks()), len(builder.Findings()))
	}

	check := builder.Checks()[0]
	if check.ID != "preflight-intent-1-v2-aircraft_exists" {
		t.Fatalf("check ID = %q", check.ID)
	}
	if check.Status != domain.PreflightStatusClear || check.Blocking || check.RuleVersion != "demo.v1" {
		t.Fatalf("check = %#v", check)
	}
	if !check.CapturedAt.Equal(now) {
		t.Fatalf("CapturedAt = %v, want %v", check.CapturedAt, now)
	}

	finding := builder.Findings()[0]
	if finding.ID != "finding-intent-1-v2-aircraft_exists" {
		t.Fatalf("finding ID = %q", finding.ID)
	}
	if finding.Status != domain.ComplianceFindingPass || finding.Severity != domain.SeverityInfo || finding.Blocking {
		t.Fatalf("finding = %#v", finding)
	}
	if finding.RuleVersion != "demo.v1" || finding.Remediation != "" {
		t.Fatalf("finding = %#v", finding)
	}
	if !finding.EvaluatedAt.Equal(now) {
		t.Fatalf("EvaluatedAt = %v, want %v", finding.EvaluatedAt, now)
	}
}

func TestBuilderBlockWritesFailFinding(t *testing.T) {
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	builder := newBuilder(testSnapshot(now))
	builder.Block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")

	if !builder.Blocked() {
		t.Fatal("block should set blocked")
	}
	check := builder.Checks()[0]
	if check.ID != "preflight-intent-1-v2-battery_soh_known" || check.Status != domain.PreflightStatusBlocked || !check.Blocking {
		t.Fatalf("check = %#v", check)
	}
	finding := builder.Findings()[0]
	if finding.ID != "finding-intent-1-v2-battery_soh_known" {
		t.Fatalf("finding ID = %q", finding.ID)
	}
	if finding.Status != domain.ComplianceFindingFail || finding.Severity != domain.SeverityCritical || !finding.Blocking {
		t.Fatalf("finding = %#v", finding)
	}
	if finding.Remediation != "record battery state of health" {
		t.Fatalf("remediation = %q", finding.Remediation)
	}
}
