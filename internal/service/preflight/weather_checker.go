package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// WeatherChecker records weather readiness from a WeatherProvider.
//
// Limitation: the current PreflightStatus / ComplianceFinding model has no
// UNKNOWN value. Provider errors are therefore recorded as blocking failures
// rather than silent clears; a future status-model PR can map them to UNKNOWN.
type WeatherChecker struct {
	provider WeatherProvider
}

func (WeatherChecker) Name() string { return "weather" }

func (c WeatherChecker) Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder) {
	result, err := c.provider.Check(ctx, snapshot)
	if err != nil {
		builder.Block(
			domain.PreflightCheckWeather,
			"demo_weather",
			"weather_provider",
			"WX-PROVIDER",
			"weather provider failed",
			"retry weather lookup; provider failure is not treated as clear",
		)
		return
	}
	if result.Clear {
		builder.Clear(domain.PreflightCheckWeather, result.Key, result.Source, result.RequirementCode, result.Summary)
		return
	}
	builder.Block(domain.PreflightCheckWeather, result.Key, result.Source, result.RequirementCode, result.Summary, result.Remediation)
}
