package preflight

import (
	"context"
	"errors"
	"testing"
)

func TestDemoWeatherProviderPreservesClearResult(t *testing.T) {
	result, err := DemoWeatherProvider{}.Check(context.Background(), testSnapshot(timeNow()))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.Clear || result.Key != "demo_weather" || result.Source != "demo_weather_provider" || result.RequirementCode != "WX-DEMO" || result.Summary != "demo weather check clear" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDemoNOTAMProviderPreservesClearResult(t *testing.T) {
	result, err := DemoNOTAMProvider{}.Check(context.Background(), testSnapshot(timeNow()))
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !result.Clear || result.Key != "demo_notam" || result.Source != "demo_notam_provider" || result.RequirementCode != "NOTAM-DEMO" || result.Summary != "demo NOTAM check clear" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDefaultConstructorInstallsDemoProviders(t *testing.T) {
	service := NewPreflightService(nil)
	wxBuilder := evaluateChecker(t, findChecker(t, service, "weather"), testSnapshot(timeNow()))
	notamBuilder := evaluateChecker(t, findChecker(t, service, "notam"), testSnapshot(timeNow()))
	wx := requireCheck(t, wxBuilder, "demo_weather", "WX-DEMO", "demo_weather_provider", false)
	notam := requireCheck(t, notamBuilder, "demo_notam", "NOTAM-DEMO", "demo_notam_provider", false)
	if wx.Summary != "demo weather check clear" || notam.Summary != "demo NOTAM check clear" {
		t.Fatalf("summaries = %q, %q", wx.Summary, notam.Summary)
	}
}

type stubWeatherProvider struct {
	called int
	result WeatherResult
	err    error
}

func (s *stubWeatherProvider) Check(_ context.Context, _ Snapshot) (WeatherResult, error) {
	s.called++
	return s.result, s.err
}

type stubNOTAMProvider struct {
	called int
	result NOTAMResult
	err    error
}

func (s *stubNOTAMProvider) Check(_ context.Context, _ Snapshot) (NOTAMResult, error) {
	s.called++
	return s.result, s.err
}

func TestInjectedWeatherProviderIsCalledAndDrivesOutput(t *testing.T) {
	stub := &stubWeatherProvider{result: WeatherResult{
		Key:             "demo_weather",
		Source:          "injected_weather",
		RequirementCode: "WX-BLOCK",
		Summary:         "weather blocks activation",
		Remediation:     "wait for conditions",
		Clear:           false,
	}}
	service := NewPreflightService(nil, WithWeatherProvider(stub))
	builder := evaluateChecker(t, findChecker(t, service, "weather"), testSnapshot(timeNow()))
	if stub.called != 1 {
		t.Fatalf("weather provider called %d times, want 1", stub.called)
	}
	check := requireCheck(t, builder, "demo_weather", "WX-BLOCK", "injected_weather", true)
	if check.Summary != "weather blocks activation" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestInjectedNOTAMProviderIsCalledAndDrivesOutput(t *testing.T) {
	stub := &stubNOTAMProvider{result: NOTAMResult{
		Key:             "demo_notam",
		Source:          "injected_notam",
		RequirementCode: "NOTAM-BLOCK",
		Summary:         "notam blocks activation",
		Remediation:     "adjust route",
		Clear:           false,
	}}
	service := NewPreflightService(nil, WithNOTAMProvider(stub))
	builder := evaluateChecker(t, findChecker(t, service, "notam"), testSnapshot(timeNow()))
	if stub.called != 1 {
		t.Fatalf("notam provider called %d times, want 1", stub.called)
	}
	check := requireCheck(t, builder, "demo_notam", "NOTAM-BLOCK", "injected_notam", true)
	if check.Summary != "notam blocks activation" {
		t.Fatalf("summary = %q", check.Summary)
	}
}

func TestWeatherProviderErrorDoesNotSilentlyClear(t *testing.T) {
	stub := &stubWeatherProvider{err: errors.New("timeout")}
	builder := evaluateChecker(t, WeatherChecker{provider: stub}, testSnapshot(timeNow()))
	check := requireCheck(t, builder, "demo_weather", "WX-PROVIDER", "weather_provider", true)
	if check.Status == "clear" || !builder.Blocked() {
		t.Fatalf("provider error produced clear/pass: %#v blocked=%v", check, builder.Blocked())
	}
}

func TestNOTAMProviderErrorDoesNotSilentlyClear(t *testing.T) {
	stub := &stubNOTAMProvider{err: errors.New("unavailable")}
	builder := evaluateChecker(t, NOTAMChecker{provider: stub}, testSnapshot(timeNow()))
	check := requireCheck(t, builder, "demo_notam", "NOTAM-PROVIDER", "notam_provider", true)
	if check.Status == "clear" || !builder.Blocked() {
		t.Fatalf("provider error produced clear/pass: %#v blocked=%v", check, builder.Blocked())
	}
}

func findChecker(t *testing.T, service *PreflightService, name string) Checker {
	t.Helper()
	for _, checker := range service.checkers {
		if checker.Name() == name {
			return checker
		}
	}
	t.Fatalf("missing checker %q", name)
	return nil
}
