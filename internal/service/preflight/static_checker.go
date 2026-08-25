package preflight

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type StaticEnvironmentChecker struct{}

func (StaticEnvironmentChecker) Name() string { return "static_environment" }

func (StaticEnvironmentChecker) Evaluate(_ context.Context, _ Snapshot, builder *Builder) {
	builder.Clear(domain.PreflightCheckWeather, "demo_weather", "demo_weather_provider", "WX-DEMO", "demo weather check clear")
	builder.Clear(domain.PreflightCheckNOTAM, "demo_notam", "demo_notam_provider", "NOTAM-DEMO", "demo NOTAM check clear")
}
