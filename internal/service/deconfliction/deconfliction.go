package deconfliction

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

const deconflictionRuleVersion = "provider-aggregate-v1"

type DeconflictionService struct {
	durable   durable.Store
	providers []airspaceprovider.Provider
	publisher airspaceprovider.Publisher
	now       func() time.Time
}

func NewDeconflictionService(
	store durable.Store,
	providers ...airspaceprovider.Provider,
) (*DeconflictionService, error) {
	return NewDeconflictionServiceWithClock(store, nil, providers...)
}

func NewDeconflictionServiceWithClock(
	store durable.Store,
	now func() time.Time,
	providers ...airspaceprovider.Provider,
) (*DeconflictionService, error) {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	configured := make([]airspaceprovider.Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			configured = append(configured, provider)
		}
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("deconfliction airspace provider is required")
	}
	service := &DeconflictionService{durable: store, providers: configured, now: now}
	for _, provider := range configured {
		if publisher, ok := provider.(airspaceprovider.Publisher); ok {
			if service.publisher != nil {
				return nil, fmt.Errorf("only one deconfliction publisher may be configured")
			}
			service.publisher = publisher
		}
	}
	return service, nil
}

func (s *DeconflictionService) CheckIntent(ctx context.Context, intentID string) (domain.DeconflictionResult, error) {
	intent, volumes, err := s.loadIntent(ctx, intentID)
	if err != nil {
		return domain.DeconflictionResult{}, err
	}

	result, _, err := s.check(ctx, intent, volumes)
	return result, err
}

func (s *DeconflictionService) check(ctx context.Context, intent domain.OperationalIntent, volumes []domain.OperationalVolume) (domain.DeconflictionResult, []airspaceprovider.OperationalIntent, error) {
	checkedAt := s.now().UTC()
	result := newResult(intent, checkedAt)
	if len(volumes) == 0 {
		result.Findings = append(result.Findings, domain.ConflictFinding{
			SourceType: domain.ConflictFindingSourceLocal,
			SourceID:   "deconfliction_service",
			Status:     domain.ConflictFindingStatusIndeterminate,
			Message:    "intent has no operational volumes to check",
		})
		finalized, err := s.finalize(ctx, result)
		return finalized, nil, err
	}

	records, providerFindings := s.discoverOperationalIntents(ctx, intent, volumes)
	result.Findings = append(result.Findings, providerFindings...)
	result.Findings = append(result.Findings, evaluateConflicts(intent, volumes, records)...)

	finalized, err := s.finalize(ctx, result)
	return finalized, records, err
}

func (s *DeconflictionService) loadIntent(ctx context.Context, intentID string) (domain.OperationalIntent, []domain.OperationalVolume, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.OperationalIntent{}, nil, fmt.Errorf("get operational intent: %w", err)
	}

	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return domain.OperationalIntent{}, nil, fmt.Errorf("list operational volumes: %w", err)
	}

	return intent, volumesForVersion(volumes, intent.Version), nil
}

func newResult(intent domain.OperationalIntent, checkedAt time.Time) domain.DeconflictionResult {
	return domain.DeconflictionResult{
		Intent:      intent,
		Posture:     domain.DeconflictionPostureClear,
		Findings:    make([]domain.ConflictFinding, 0),
		CheckedAt:   checkedAt,
		RuleVersion: deconflictionRuleVersion,
	}
}

func (s *DeconflictionService) discoverOperationalIntents(
	ctx context.Context,
	intent domain.OperationalIntent,
	volumes []domain.OperationalVolume,
) ([]airspaceprovider.OperationalIntent, []domain.ConflictFinding) {
	records := make([]airspaceprovider.OperationalIntent, 0)
	findings := make([]domain.ConflictFinding, 0)
	seen := make(map[string]struct{})

	for _, provider := range s.providers {
		discovered, err := provider.FindOperationalIntents(ctx, airspaceprovider.Query{
			Intent: intent, Volumes: volumes,
		})
		if err != nil {
			findings = append(findings, domain.ConflictFinding{
				Status:   domain.ConflictFindingStatusIndeterminate,
				Message:  fmt.Sprintf("airspace provider %q could not be queried: %v", provider.ID(), err),
				SourceID: provider.ID(),
			})
		}

		for _, record := range discovered {
			record.Source.ProviderID = provider.ID()
			key := sourceKey(record.Source)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			records = append(records, record)
		}
	}

	return records, findings
}

func (s *DeconflictionService) finalize(ctx context.Context, result domain.DeconflictionResult) (domain.DeconflictionResult, error) {
	if len(result.Findings) == 0 {
		result.Findings = append(result.Findings, domain.ConflictFinding{
			SourceType: domain.ConflictFindingSourceLocal,
			SourceID:   "deconfliction_service",
			Status:     domain.ConflictFindingStatusClear,
			Message:    "no airspace provider reported a conflict",
		})
	}

	for index, finding := range result.Findings {
		finding = normalizeFinding(result.Intent, finding, finding.SourceID, result.CheckedAt)
		result.Findings[index] = finding
		result.Posture = maxPosture(result.Posture, finding.Status)
	}

	if err := s.durable.ReplaceConflictFindings(ctx, result.Intent.ID, result.Intent.Version, deconflictionRuleVersion, result.Findings); err != nil {
		return domain.DeconflictionResult{}, fmt.Errorf("replace conflict findings: %w", err)
	}

	return result, nil
}

func sourceKey(source airspaceprovider.Source) string {
	return strings.Join([]string{
		source.ProviderID, source.ReferenceID, fmt.Sprintf("%d", source.Version),
	}, "\x00")
}

func (s *DeconflictionService) ListConflictFindings(ctx context.Context, intentID string) ([]domain.ConflictFinding, error) {
	if strings.TrimSpace(intentID) == "" {
		return nil, fmt.Errorf("validation failed: intent_id is required")
	}
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return nil, fmt.Errorf("get operational intent: %w", err)
	}
	findings, err := s.durable.ListConflictFindings(ctx, intent.ID, intent.Version)
	if err != nil {
		return nil, fmt.Errorf("list conflict findings: %w", err)
	}
	return findings, nil
}

func normalizeFinding(intent domain.OperationalIntent, finding domain.ConflictFinding, fallbackSourceID string, checkedAt time.Time) domain.ConflictFinding {
	finding.IntentID, finding.IntentVersion = intent.ID, intent.Version
	finding.OperatorID, finding.AircraftID = intent.OperatorID, intent.AircraftID
	if finding.SourceID == "" {
		finding.SourceID = fallbackSourceID
	}
	if finding.SourceType == "" {
		finding.SourceType = domain.ConflictFindingSourceExternal
	}
	if finding.ID == "" {
		finding.ID = strings.Join([]string{
			"conflict", intent.ID, fmt.Sprintf("v%d", intent.Version), string(finding.Status),
			emptyID(finding.SourceID), emptyID(finding.VolumeID),
			emptyID(finding.ConflictingIntentID), emptyID(finding.ConflictingVolumeID),
		}, "-")
	}
	if finding.EvaluatedAt.IsZero() {
		finding.EvaluatedAt = checkedAt
	}
	finding.RuleVersion = deconflictionRuleVersion
	if finding.Severity == "" {
		finding.Severity = conflictSeverity(finding.Status)
	}
	finding.Blocking = finding.Status != domain.ConflictFindingStatusClear
	return finding
}

func volumesForVersion(volumes []domain.OperationalVolume, version int) []domain.OperationalVolume {
	filtered := make([]domain.OperationalVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.IntentVersion == version {
			filtered = append(filtered, volume)
		}
	}
	return filtered
}

func maxPosture(current domain.DeconflictionPosture, status domain.ConflictFindingStatus) domain.DeconflictionPosture {
	next := postureForFinding(status)
	if postureRank(next) > postureRank(current) {
		return next
	}
	return current
}

func postureForFinding(status domain.ConflictFindingStatus) domain.DeconflictionPosture {
	switch status {
	case domain.ConflictFindingStatusConflict:
		return domain.DeconflictionPostureConflict
	case domain.ConflictFindingStatusPotentialConflict:
		return domain.DeconflictionPosturePotentialConflict
	case domain.ConflictFindingStatusIndeterminate:
		return domain.DeconflictionPostureIndeterminate
	default:
		return domain.DeconflictionPostureClear
	}
}

func postureRank(posture domain.DeconflictionPosture) int {
	switch posture {
	case domain.DeconflictionPostureConflict:
		return 4
	case domain.DeconflictionPostureIndeterminate:
		return 3
	case domain.DeconflictionPosturePotentialConflict:
		return 2
	default:
		return 1
	}
}

func conflictSeverity(status domain.ConflictFindingStatus) domain.Severity {
	switch status {
	case domain.ConflictFindingStatusConflict:
		return domain.SeverityCritical
	case domain.ConflictFindingStatusPotentialConflict, domain.ConflictFindingStatusIndeterminate:
		return domain.SeverityWarning
	default:
		return domain.SeverityInfo
	}
}

func emptyID(value string) string {
	if value == "" {
		return "none"
	}
	return value
}
