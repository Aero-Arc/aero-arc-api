package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

var (
	ErrInvalidTransition = errors.New("invalid intent lifecycle transition")
	ErrActivationBlocked = errors.New("intent activation blocked")
	ErrValidation        = errors.New("validation failed")
)

type IntentService struct {
	durable       durable.Store
	now           func() time.Time
	deconfliction DeconflictionChecker
}

type DeconflictionChecker interface {
	CheckIntent(ctx context.Context, intentID string) (domain.DeconflictionResult, error)
}

type CreateIntentRequest struct {
	ID                  string                    `json:"id,omitempty"`
	OperatorID          string                    `json:"operator_id,omitempty"`
	AircraftID          string                    `json:"aircraft_id"`
	AuthorizationID     string                    `json:"authorization_id,omitempty"`
	Name                string                    `json:"name"`
	Summary             string                    `json:"summary"`
	UseCase             string                    `json:"use_case,omitempty"`
	AuthorizationPath   domain.AuthorizationPath  `json:"authorization_path,omitempty"`
	PopulationCategory  domain.PopulationCategory `json:"population_category,omitempty"`
	ConformanceRequired bool                      `json:"conformance_required"`
	OperatingAreaID     string                    `json:"operating_area_id,omitempty"`
	RouteSummary        string                    `json:"route_summary,omitempty"`
	PlannedStartAt      time.Time                 `json:"planned_start_at"`
	PlannedEndAt        time.Time                 `json:"planned_end_at"`
	MinAltitudeFtAGL    *float64                  `json:"min_altitude_ft_agl,omitempty"`
	MaxAltitudeFtAGL    *float64                  `json:"max_altitude_ft_agl,omitempty"`
	SupervisorID        string                    `json:"supervisor_id,omitempty"`
	FlightCoordinatorID string                    `json:"flight_coordinator_id,omitempty"`
}

type AddOperationalVolumeRequest struct {
	ID           string                       `json:"id,omitempty"`
	Sequence     int                          `json:"sequence"`
	GeometryURI  string                       `json:"geometry_uri,omitempty"`
	GeoJSON      string                       `json:"geojson,omitempty"`
	MinAltitudeM *float64                     `json:"min_altitude_m"`
	MaxAltitudeM *float64                     `json:"max_altitude_m"`
	AltitudeRef  domain.AltitudeReference     `json:"altitude_ref,omitempty"`
	StartsAt     time.Time                    `json:"starts_at"`
	EndsAt       time.Time                    `json:"ends_at"`
	BufferMeters *float64                     `json:"buffer_meters,omitempty"`
	VolumeType   domain.OperationalVolumeType `json:"volume_type,omitempty"`
}

func NewIntentService(durableStore durable.Store, deconfliction ...DeconflictionChecker) *IntentService {
	return NewIntentServiceWithClock(durableStore, nil, deconfliction...)
}

func NewIntentServiceWithClock(durableStore durable.Store, now func() time.Time, deconfliction ...DeconflictionChecker) *IntentService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	service := &IntentService{
		durable:       durableStore,
		now:           now,
		deconfliction: NewDeconflictionServiceWithClock(durableStore, now),
	}
	if len(deconfliction) > 0 {
		service.deconfliction = deconfliction[0]
	}
	return service
}

func (s *IntentService) CreateIntent(ctx context.Context, req CreateIntentRequest) (domain.OperationalIntent, error) {
	now := s.now().UTC()
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("intent-%d", now.UnixNano())
	}
	if strings.TrimSpace(req.AircraftID) == "" {
		return domain.OperationalIntent{}, fmt.Errorf("%w: aircraft_id is required", ErrValidation)
	}
	if req.PlannedStartAt.IsZero() || req.PlannedEndAt.IsZero() {
		return domain.OperationalIntent{}, fmt.Errorf("%w: planned start and end are required", ErrValidation)
	}

	intent := domain.OperationalIntent{
		ID:                  id,
		OperatorID:          req.OperatorID,
		AircraftID:          req.AircraftID,
		AuthorizationID:     req.AuthorizationID,
		Version:             1,
		Name:                req.Name,
		Summary:             req.Summary,
		UseCase:             req.UseCase,
		AuthorizationPath:   req.AuthorizationPath,
		PopulationCategory:  req.PopulationCategory,
		Status:              domain.IntentStatusDraft,
		ConformanceRequired: req.ConformanceRequired,
		OperatingAreaID:     req.OperatingAreaID,
		RouteSummary:        req.RouteSummary,
		PlannedStartAt:      req.PlannedStartAt.UTC(),
		PlannedEndAt:        req.PlannedEndAt.UTC(),
		MinAltitudeFtAGL:    req.MinAltitudeFtAGL,
		MaxAltitudeFtAGL:    req.MaxAltitudeFtAGL,
		SupervisorID:        req.SupervisorID,
		FlightCoordinatorID: req.FlightCoordinatorID,
		UpdatedAt:           now,
	}
	if intent.AuthorizationPath == "" {
		intent.AuthorizationPath = domain.AuthorizationPathUnknown
	}
	if intent.PopulationCategory == "" {
		intent.PopulationCategory = domain.PopulationCategoryUnknown
	}

	if err := s.durable.CreateOperationalIntent(ctx, intent); err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("create operational intent: %w", err)
	}
	return intent, nil
}

func (s *IntentService) GetIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("get operational intent: %w", err)
	}
	return intent, nil
}

func (s *IntentService) AddOperationalVolume(ctx context.Context, intentID string, req AddOperationalVolumeRequest) (domain.OperationalVolume, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.OperationalVolume{}, fmt.Errorf("get operational intent: %w", err)
	}
	if intent.Status != domain.IntentStatusDraft {
		return domain.OperationalVolume{}, fmt.Errorf("%w: operational volumes are locked after draft status (%s)", ErrInvalidTransition, intent.Status)
	}

	now := s.now().UTC()
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("%s-volume-%d", intent.ID, now.UnixNano())
	}
	if req.MinAltitudeM == nil || req.MaxAltitudeM == nil {
		return domain.OperationalVolume{}, fmt.Errorf("%w: min_altitude_m and max_altitude_m are required", ErrValidation)
	}
	if req.AltitudeRef == "" {
		return domain.OperationalVolume{}, fmt.Errorf("%w: altitude_ref is required", ErrValidation)
	}
	volume := domain.OperationalVolume{
		ID:            id,
		OperatorID:    intent.OperatorID,
		IntentID:      intent.ID,
		IntentVersion: intent.Version,
		Sequence:      req.Sequence,
		GeometryURI:   req.GeometryURI,
		GeoJSON:       req.GeoJSON,
		MinAltitudeM:  *req.MinAltitudeM,
		MaxAltitudeM:  *req.MaxAltitudeM,
		AltitudeRef:   req.AltitudeRef,
		StartsAt:      req.StartsAt.UTC(),
		EndsAt:        req.EndsAt.UTC(),
		BufferMeters:  req.BufferMeters,
		VolumeType:    req.VolumeType,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.durable.RecordOperationalVolume(ctx, volume); err != nil {
		return domain.OperationalVolume{}, fmt.Errorf("record operational volume: %w", err)
	}
	return volume, nil
}

func (s *IntentService) SubmitIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error) {
	return s.transitionIntent(ctx, intentID, domain.IntentStatusSubmitted, map[domain.IntentStatus]bool{
		domain.IntentStatusDraft: true,
	}, func(intent *domain.OperationalIntent, now time.Time) {
		intent.SubmittedAt = &now
	})
}

func (s *IntentService) AcceptIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error) {
	return s.transitionIntent(ctx, intentID, domain.IntentStatusAccepted, map[domain.IntentStatus]bool{
		domain.IntentStatusSubmitted: true,
		domain.IntentStatusReview:    true,
	}, func(intent *domain.OperationalIntent, now time.Time) {
		intent.AcceptedAt = &now
	})
}

func (s *IntentService) ActivateIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("get operational intent: %w", err)
	}
	if intent.Status != domain.IntentStatusAccepted {
		return domain.OperationalIntent{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, intent.Status, domain.IntentStatusActive)
	}
	if s.deconfliction != nil {
		result, err := s.deconfliction.CheckIntent(ctx, intent.ID)
		if err != nil {
			return domain.OperationalIntent{}, fmt.Errorf("check deconfliction: %w", err)
		}
		if result.Posture != domain.DeconflictionPostureClear {
			return domain.OperationalIntent{}, fmt.Errorf("%w: deconfliction posture is %s", ErrActivationBlocked, result.Posture)
		}
	}
	if err := s.activationReadiness(ctx, intent); err != nil {
		return domain.OperationalIntent{}, err
	}

	now := s.now().UTC()
	intent.Status = domain.IntentStatusActive
	intent.ActivatedAt = &now
	intent.UpdatedAt = now
	if err := s.durable.UpdateOperationalIntent(ctx, intent); err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("update operational intent: %w", err)
	}
	return intent, nil
}

func (s *IntentService) CompleteIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error) {
	return s.transitionIntent(ctx, intentID, domain.IntentStatusComplete, map[domain.IntentStatus]bool{
		domain.IntentStatusActive: true,
	}, func(intent *domain.OperationalIntent, now time.Time) {
		intent.CompletedAt = &now
	})
}

func (s *IntentService) CancelIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error) {
	return s.transitionIntent(ctx, intentID, domain.IntentStatusCanceled, map[domain.IntentStatus]bool{
		domain.IntentStatusDraft:     true,
		domain.IntentStatusSubmitted: true,
		domain.IntentStatusReview:    true,
		domain.IntentStatusAccepted:  true,
		domain.IntentStatusActive:    true,
	}, func(intent *domain.OperationalIntent, now time.Time) {
		intent.CanceledAt = &now
	})
}

func (s *IntentService) transitionIntent(ctx context.Context, intentID string, next domain.IntentStatus, allowed map[domain.IntentStatus]bool, mutate func(*domain.OperationalIntent, time.Time)) (domain.OperationalIntent, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("get operational intent: %w", err)
	}
	if !allowed[intent.Status] {
		return domain.OperationalIntent{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, intent.Status, next)
	}

	now := s.now().UTC()
	intent.Status = next
	intent.UpdatedAt = now
	if mutate != nil {
		mutate(&intent, now)
	}
	if err := s.durable.UpdateOperationalIntent(ctx, intent); err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("update operational intent: %w", err)
	}
	return intent, nil
}

func (s *IntentService) activationReadiness(ctx context.Context, intent domain.OperationalIntent) error {
	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return fmt.Errorf("list operational volumes: %w", err)
	}
	volumes = volumesForVersion(volumes, intent.Version)
	if len(volumes) == 0 {
		return fmt.Errorf("%w: at least one operational volume is required", ErrActivationBlocked)
	}

	checks, err := s.durable.ListPreflightChecks(ctx, intent.ID)
	if err != nil {
		return fmt.Errorf("list preflight checks: %w", err)
	}
	checks = preflightChecksForVersion(checks, intent.Version)
	if len(checks) == 0 {
		return fmt.Errorf("%w: current preflight checks are required", ErrActivationBlocked)
	}
	for _, check := range checks {
		if check.Blocking && (check.Status == domain.PreflightStatusBlocked || check.Status == domain.PreflightStatusAction) {
			return fmt.Errorf("%w: blocking preflight check %s", ErrActivationBlocked, check.ID)
		}
	}

	findings, err := s.durable.ListComplianceFindingsForIntent(ctx, intent.ID)
	if err != nil {
		return fmt.Errorf("list compliance findings: %w", err)
	}
	findings = complianceFindingsForVersion(findings, intent.Version)
	for _, finding := range findings {
		if finding.Blocking && (finding.Status == domain.ComplianceFindingFail || finding.Status == domain.ComplianceFindingReview) {
			return fmt.Errorf("%w: blocking compliance finding %s", ErrActivationBlocked, finding.ID)
		}
	}
	return nil
}
