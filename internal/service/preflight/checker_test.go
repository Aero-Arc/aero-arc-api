package preflight

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func timeNow() time.Time {
	return time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
}

func evaluateChecker(t *testing.T, checker Checker, snapshot Snapshot) *Builder {
	t.Helper()
	builder := newBuilder(snapshot)
	checker.Evaluate(context.Background(), snapshot, builder)
	return builder
}

func requireCheck(t *testing.T, builder *Builder, key, requirementCode, source string, blocked bool) domain.PreflightCheck {
	t.Helper()
	wantID := fmt.Sprintf("preflight-%s-v%d-%s", builder.snapshot.Intent.ID, builder.snapshot.Intent.Version, key)
	for _, check := range builder.Checks() {
		if check.ID != wantID {
			continue
		}
		if check.RequirementCode != requirementCode || check.Source != source || check.RuleVersion != "demo.v1" || check.Blocking != blocked {
			t.Fatalf("check %#v, want code=%s source=%s blocked=%v demo.v1", check, requirementCode, source, blocked)
		}
		return check
	}
	t.Fatalf("missing check %s in %#v", wantID, builder.Checks())
	return domain.PreflightCheck{}
}

func requireNoCheck(t *testing.T, builder *Builder, key string) {
	t.Helper()
	wantID := fmt.Sprintf("preflight-%s-v%d-%s", builder.snapshot.Intent.ID, builder.snapshot.Intent.Version, key)
	for _, check := range builder.Checks() {
		if check.ID == wantID {
			t.Fatalf("unexpected check %s", check.ID)
		}
	}
}

func TestDefaultCheckerOrder(t *testing.T) {
	service := NewPreflightService(nil)
	want := []string{"aircraft", "remote_id", "intent_volume", "battery", "maintenance", "static_environment"}
	if len(service.checkers) != len(want) {
		t.Fatalf("checkers = %d, want %d", len(service.checkers), len(want))
	}
	for i, name := range want {
		if got := service.checkers[i].Name(); got != name {
			t.Fatalf("checkers[%d] = %q, want %q", i, got, name)
		}
	}
}
