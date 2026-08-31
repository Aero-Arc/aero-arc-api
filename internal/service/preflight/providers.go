package preflight

import "context"

// WeatherProvider supplies weather readiness evidence for a Snapshot.
// Provider failures must return an error; callers must not treat that as clear.
type WeatherProvider interface {
	Check(ctx context.Context, snapshot Snapshot) (WeatherResult, error)
}

// NOTAMProvider supplies NOTAM/restriction readiness evidence for a Snapshot.
// Provider failures must return an error; callers must not treat that as clear.
type NOTAMProvider interface {
	Check(ctx context.Context, snapshot Snapshot) (NOTAMResult, error)
}

// WeatherResult is domain-shaped weather evidence consumed by WeatherChecker.
type WeatherResult struct {
	Key             string
	Source          string
	RequirementCode string
	Summary         string
	Remediation     string
	Clear           bool
}

// NOTAMResult is domain-shaped NOTAM evidence consumed by NOTAMChecker.
type NOTAMResult struct {
	Key             string
	Source          string
	RequirementCode string
	Summary         string
	Remediation     string
	Clear           bool
}

// DemoWeatherProvider reproduces the legacy always-clear demo weather check.
type DemoWeatherProvider struct{}

func (DemoWeatherProvider) Check(_ context.Context, _ Snapshot) (WeatherResult, error) {
	return WeatherResult{
		Key:             "demo_weather",
		Source:          "demo_weather_provider",
		RequirementCode: "WX-DEMO",
		Summary:         "demo weather check clear",
		Clear:           true,
	}, nil
}

// DemoNOTAMProvider reproduces the legacy always-clear demo NOTAM check.
type DemoNOTAMProvider struct{}

func (DemoNOTAMProvider) Check(_ context.Context, _ Snapshot) (NOTAMResult, error) {
	return NOTAMResult{
		Key:             "demo_notam",
		Source:          "demo_notam_provider",
		RequirementCode: "NOTAM-DEMO",
		Summary:         "demo NOTAM check clear",
		Clear:           true,
	}, nil
}
