package preflight

import (
	"context"
	"testing"
)

func TestWeatherCheckerUsesDemoProviderClear(t *testing.T) {
	builder := evaluateChecker(t, WeatherChecker{provider: DemoWeatherProvider{}}, testSnapshot(timeNow()))
	check := requireCheck(t, builder, "demo_weather", "WX-DEMO", "demo_weather_provider", false)
	if check.Summary != "demo weather check clear" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestNOTAMCheckerUsesDemoProviderClear(t *testing.T) {
	builder := evaluateChecker(t, NOTAMChecker{provider: DemoNOTAMProvider{}}, testSnapshot(timeNow()))
	check := requireCheck(t, builder, "demo_notam", "NOTAM-DEMO", "demo_notam_provider", false)
	if check.Summary != "demo NOTAM check clear" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestWeatherBeforeNOTAMOrdering(t *testing.T) {
	service := NewPreflightService(nil)
	builder := newBuilder(testSnapshot(timeNow()))
	for _, checker := range service.checkers {
		if checker.Name() != "weather" && checker.Name() != "notam" {
			continue
		}
		checker.Evaluate(context.Background(), builder.snapshot, builder)
	}
	if len(builder.Checks()) != 2 {
		t.Fatalf("checks = %#v, want weather then notam", builder.Checks())
	}
	if builder.Checks()[0].RequirementCode != "WX-DEMO" || builder.Checks()[1].RequirementCode != "NOTAM-DEMO" {
		t.Fatalf("order = %#v", builder.Checks())
	}
}
