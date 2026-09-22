package preflight

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type PreflightService struct {
	durable  durable.Store
	now      func() time.Time
	checkers []Checker
}

type PreflightEvaluation struct {
	Intent   domain.OperationalIntent   `json:"intent"`
	Checks   []domain.PreflightCheck    `json:"checks"`
	Findings []domain.ComplianceFinding `json:"findings"`
	Blocked  bool                       `json:"blocked"`
}

// Option configures optional PreflightService dependencies.
type Option func(*preflightOptions)

type preflightOptions struct {
	weather WeatherProvider
	notam   NOTAMProvider
}

// WithWeatherProvider injects a WeatherProvider. Nil is ignored.
func WithWeatherProvider(provider WeatherProvider) Option {
	return func(opts *preflightOptions) {
		if provider != nil {
			opts.weather = provider
		}
	}
}

// WithNOTAMProvider injects a NOTAMProvider. Nil is ignored.
func WithNOTAMProvider(provider NOTAMProvider) Option {
	return func(opts *preflightOptions) {
		if provider != nil {
			opts.notam = provider
		}
	}
}

// NewPreflightService constructs service from the supplied configuration and dependencies.
//
// Parameters:
//   - durableStore: is the durable.Store value supplied to NewPreflightService.
//   - opts: optional provider overrides; defaults install demo weather/NOTAM providers.
//
// Returns:
//   - result: is the *PreflightService value produced by NewPreflightService.
func NewPreflightService(durableStore durable.Store, opts ...Option) *PreflightService {
	return NewPreflightServiceWithClock(durableStore, nil, opts...)
}

// NewPreflightServiceWithClock constructs service from the supplied configuration and dependencies.
//
// Parameters:
//   - durableStore: is the durable.Store value supplied to NewPreflightServiceWithClock.
//   - now: supplies the event or wall-clock timestamp used by the operation.
//   - opts: optional provider overrides; defaults install demo weather/NOTAM providers.
//
// Returns:
//   - result: is the *PreflightService value produced by NewPreflightServiceWithClock.
func NewPreflightServiceWithClock(durableStore durable.Store, now func() time.Time, opts ...Option) *PreflightService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	options := preflightOptions{
		weather: DemoWeatherProvider{},
		notam:   DemoNOTAMProvider{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return &PreflightService{
		durable: durableStore,
		now:     now,
		checkers: []Checker{
			AircraftChecker{},
			RemoteIDChecker{},
			IntentVolumeChecker{},
			BatteryChecker{durable: durableStore},
			MaintenanceChecker{durable: durableStore},
			DeconflictionChecker{},
			WeatherChecker{provider: options.weather},
			NOTAMChecker{provider: options.notam},
		},
	}
}

// EvaluateIntent runs the current preflight policy against an operational
// intent and records the resulting checks.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the PreflightEvaluation value produced by EvaluateIntent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *PreflightService) EvaluateIntent(ctx context.Context, intentID string) (PreflightEvaluation, error) {
	snapshot, err := s.loadSnapshot(ctx, intentID)
	if err != nil {
		return PreflightEvaluation{}, err
	}

	builder := newBuilder(snapshot)
	for _, checker := range s.checkers {
		checker.Evaluate(ctx, snapshot, builder)
	}

	for _, check := range builder.Checks() {
		if err := s.durable.RecordPreflightCheck(ctx, check); err != nil {
			return PreflightEvaluation{}, fmt.Errorf("record preflight check: %w", err)
		}
	}
	for _, finding := range builder.Findings() {
		if err := s.durable.RecordComplianceFinding(ctx, finding); err != nil {
			return PreflightEvaluation{}, fmt.Errorf("record compliance finding: %w", err)
		}
	}

	return PreflightEvaluation{
		Intent:   snapshot.Intent,
		Checks:   builder.Checks(),
		Findings: builder.Findings(),
		Blocked:  builder.Blocked(),
	}, nil
}
