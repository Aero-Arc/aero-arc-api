package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type Store struct {
	mu                   sync.RWMutex
	operators            map[string]domain.Operator
	aircraft             map[string]domain.Aircraft
	batteries            map[string]domain.Battery
	batteryInstallations []domain.BatteryInstallation
	operatingProfiles    map[string]domain.AircraftOperatingProfile
	operatingLimits      map[string]domain.OperatingLimit
	maintenanceEvents    []domain.MaintenanceEvent
	operationalIntents   map[string]domain.OperationalIntent
	operationalVolumes   map[string]domain.OperationalVolume
	authorizations       map[string]domain.RegulatoryAuthorization
	preflightChecks      []domain.PreflightCheck
	flightRecords        map[string]domain.FlightRecord
	conformanceEvents    []domain.ConformanceEvent
	conformanceSummaries map[string]domain.ConformanceSummary
	evidenceRecords      map[string]domain.EvidenceRecord
	reportabilityReviews []domain.ReportabilityReview
	complianceFindings   []domain.ComplianceFinding
	conflictFindings     []domain.ConflictFinding
	personnel            map[string]domain.OperationsPersonnel
	personnelAssignments []domain.PersonnelAssignment
}

var _ durable.OperationalStore = (*Store)(nil)

func NewStore() *Store {
	return &Store{
		operators:            make(map[string]domain.Operator),
		aircraft:             make(map[string]domain.Aircraft),
		batteries:            make(map[string]domain.Battery),
		operatingProfiles:    make(map[string]domain.AircraftOperatingProfile),
		operatingLimits:      make(map[string]domain.OperatingLimit),
		operationalIntents:   make(map[string]domain.OperationalIntent),
		operationalVolumes:   make(map[string]domain.OperationalVolume),
		authorizations:       make(map[string]domain.RegulatoryAuthorization),
		flightRecords:        make(map[string]domain.FlightRecord),
		conformanceSummaries: make(map[string]domain.ConformanceSummary),
		evidenceRecords:      make(map[string]domain.EvidenceRecord),
		personnel:            make(map[string]domain.OperationsPersonnel),
	}
}

func (s *Store) UpsertOperator(_ context.Context, operator domain.Operator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operators[operator.ID] = operator
	return nil
}

func (s *Store) GetOperator(_ context.Context, operatorID string) (domain.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operator, ok := s.operators[operatorID]
	if !ok {
		return domain.Operator{}, durable.ErrNotFound
	}
	return operator, nil
}

func (s *Store) ListOperators(_ context.Context) ([]domain.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operators := make([]domain.Operator, 0, len(s.operators))
	for _, item := range s.operators {
		operators = append(operators, item)
	}
	sort.Slice(operators, func(i, j int) bool { return operators[i].Name < operators[j].Name })
	return operators, nil
}

func (s *Store) CreateAircraft(_ context.Context, aircraft domain.Aircraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aircraft[aircraft.ID] = aircraft
	return nil
}

func (s *Store) GetAircraft(_ context.Context, aircraftID string) (domain.Aircraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	aircraft, ok := s.aircraft[aircraftID]
	if !ok {
		return domain.Aircraft{}, durable.ErrNotFound
	}
	return aircraft, nil
}

func (s *Store) ListAircraft(_ context.Context) ([]domain.Aircraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	aircraft := make([]domain.Aircraft, 0, len(s.aircraft))
	for _, item := range s.aircraft {
		aircraft = append(aircraft, item)
	}
	sort.Slice(aircraft, func(i, j int) bool { return aircraft[i].ID < aircraft[j].ID })
	return aircraft, nil
}

func (s *Store) CreateBattery(_ context.Context, battery domain.Battery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batteries[battery.ID] = battery
	return nil
}

func (s *Store) GetBattery(_ context.Context, batteryID string) (domain.Battery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	battery, ok := s.batteries[batteryID]
	if !ok {
		return domain.Battery{}, durable.ErrNotFound
	}
	return battery, nil
}

func (s *Store) ListBatteries(_ context.Context) ([]domain.Battery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batteries := make([]domain.Battery, 0, len(s.batteries))
	for _, item := range s.batteries {
		batteries = append(batteries, item)
	}
	sort.Slice(batteries, func(i, j int) bool { return batteries[i].ID < batteries[j].ID })
	return batteries, nil
}

func (s *Store) RecordBatteryInstallation(_ context.Context, installation domain.BatteryInstallation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batteryInstallations = append(s.batteryInstallations, installation)
	return nil
}

func (s *Store) GetActiveBatteryInstallation(_ context.Context, aircraftID string) (*domain.BatteryInstallation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var active *domain.BatteryInstallation
	for _, installation := range s.batteryInstallations {
		if installation.AircraftID != aircraftID || installation.RemovedAt != nil {
			continue
		}
		item := installation
		if active == nil || item.InstalledAt.After(active.InstalledAt) {
			active = &item
		}
	}
	return active, nil
}

func (s *Store) UpsertAircraftOperatingProfile(_ context.Context, profile domain.AircraftOperatingProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatingProfiles[profile.AircraftID] = profile
	return nil
}

func (s *Store) GetAircraftOperatingProfile(_ context.Context, aircraftID string) (*domain.AircraftOperatingProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.operatingProfiles[aircraftID]
	if !ok {
		return nil, nil
	}
	return &profile, nil
}

func (s *Store) ListOperatingLimits(_ context.Context, aircraftID string) ([]domain.OperatingLimit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limits := make([]domain.OperatingLimit, 0)
	for _, limit := range s.operatingLimits {
		if limit.AircraftID == aircraftID {
			limits = append(limits, limit)
		}
	}
	sort.Slice(limits, func(i, j int) bool { return limits[i].Name < limits[j].Name })
	return limits, nil
}

func (s *Store) UpsertOperatingLimit(_ context.Context, limit domain.OperatingLimit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatingLimits[limit.ID] = limit
	return nil
}

func (s *Store) RecordMaintenanceEvent(_ context.Context, event domain.MaintenanceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenanceEvents = append(s.maintenanceEvents, event)
	return nil
}

func (s *Store) ListMaintenanceEvents(_ context.Context, aircraftID string) ([]domain.MaintenanceEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]domain.MaintenanceEvent, 0)
	for _, event := range s.maintenanceEvents {
		if aircraftID == "" || event.AircraftID == aircraftID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OpenedAt.After(events[j].OpenedAt) })
	return events, nil
}

func (s *Store) CreateOperationalIntent(_ context.Context, intent domain.OperationalIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := operationalIntentKey(intent.ID, intent.Version)
	if _, exists := s.operationalIntents[key]; exists {
		return durable.ErrAlreadyExists
	}
	intent.Revision = 0
	s.operationalIntents[key] = intent
	return nil
}

func (s *Store) UpdateOperationalIntent(_ context.Context, intent domain.OperationalIntent, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.latestOperationalIntent(intent.ID)
	if !exists {
		return durable.ErrNotFound
	}
	if current.Version != intent.Version || current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	intent.Revision = expectedRevision + 1
	s.operationalIntents[operationalIntentKey(intent.ID, intent.Version)] = intent
	return nil
}

func (s *Store) GetOperationalIntent(_ context.Context, intentID string) (domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.latestOperationalIntent(intentID)
	if !ok {
		return domain.OperationalIntent{}, durable.ErrNotFound
	}
	return intent, nil
}

func (s *Store) GetOperationalIntentVersion(_ context.Context, intentID string, version int) (domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.operationalIntents[operationalIntentKey(intentID, version)]
	if !ok {
		return domain.OperationalIntent{}, durable.ErrNotFound
	}
	return intent, nil
}

func (s *Store) ListOperationalIntents(_ context.Context, aircraftID string) ([]domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intents := make([]domain.OperationalIntent, 0)
	latestByID := make(map[string]domain.OperationalIntent)
	for _, intent := range s.operationalIntents {
		current, ok := latestByID[intent.ID]
		if !ok || intent.Version > current.Version || (intent.Version == current.Version && intent.UpdatedAt.After(current.UpdatedAt)) {
			latestByID[intent.ID] = intent
		}
	}
	for _, intent := range latestByID {
		if aircraftID == "" || intent.AircraftID == aircraftID {
			intents = append(intents, intent)
		}
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].PlannedStartAt.Before(intents[j].PlannedStartAt) })
	return intents, nil
}

func (s *Store) ListOperationalIntentVersions(_ context.Context, intentID string) ([]domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intents := make([]domain.OperationalIntent, 0)
	for _, intent := range s.operationalIntents {
		if intent.ID == intentID {
			intents = append(intents, intent)
		}
	}
	sort.Slice(intents, func(i, j int) bool {
		if intents[i].Version == intents[j].Version {
			return intents[i].UpdatedAt.Before(intents[j].UpdatedAt)
		}
		return intents[i].Version < intents[j].Version
	})
	return intents, nil
}

func (s *Store) RecordOperationalVolume(_ context.Context, volume domain.OperationalVolume) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operationalVolumes[operationalVolumeKey(volume)] = volume
	return nil
}

func (s *Store) ReplaceOperationalVolumes(_ context.Context, intentID string, intentVersion int, volumes []domain.OperationalVolume) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, volume := range volumes {
		if volume.IntentID != intentID || volume.IntentVersion != intentVersion {
			return fmt.Errorf("operational volume %q is outside replacement scope", volume.ID)
		}
	}
	for key, volume := range s.operationalVolumes {
		if volume.IntentID == intentID && volume.IntentVersion == intentVersion {
			delete(s.operationalVolumes, key)
		}
	}
	for _, volume := range volumes {
		s.operationalVolumes[operationalVolumeKey(volume)] = volume
	}
	return nil
}

func (s *Store) ReplaceOperationalIntent(
	_ context.Context,
	expectedVersion int,
	expectedRevision int64,
	intent domain.OperationalIntent,
	volumes []domain.OperationalVolume,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if intent.Version != expectedVersion && intent.Version != expectedVersion+1 {
		return durable.ErrVersionConflict
	}
	for _, volume := range volumes {
		if volume.IntentID != intent.ID || volume.IntentVersion != intent.Version {
			return fmt.Errorf("operational volume %q is outside replacement scope", volume.ID)
		}
	}
	current, ok := s.latestOperationalIntent(intent.ID)
	if !ok {
		return durable.ErrNotFound
	}
	if current.Version != expectedVersion {
		return durable.ErrVersionConflict
	}
	if current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	if intent.Version == current.Version {
		intent.Revision = expectedRevision + 1
	} else {
		intent.Revision = 0
	}
	s.operationalIntents[operationalIntentKey(intent.ID, intent.Version)] = intent
	for key, volume := range s.operationalVolumes {
		if volume.IntentID == intent.ID && volume.IntentVersion == intent.Version {
			delete(s.operationalVolumes, key)
		}
	}
	for _, volume := range volumes {
		s.operationalVolumes[operationalVolumeKey(volume)] = volume
	}
	return nil
}

func (s *Store) ListOperationalVolumes(_ context.Context, intentID string) ([]domain.OperationalVolume, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	volumes := make([]domain.OperationalVolume, 0)
	for _, volume := range s.operationalVolumes {
		if intentID == "" || volume.IntentID == intentID {
			volumes = append(volumes, volume)
		}
	}
	sort.Slice(volumes, func(i, j int) bool {
		if volumes[i].Sequence == volumes[j].Sequence {
			return volumes[i].StartsAt.Before(volumes[j].StartsAt)
		}
		return volumes[i].Sequence < volumes[j].Sequence
	})
	return volumes, nil
}

func operationalVolumeKey(volume domain.OperationalVolume) string {
	return volume.IntentID + ":" + strconv.Itoa(volume.IntentVersion) + ":" + volume.ID
}

func operationalIntentKey(intentID string, version int) string {
	return intentID + ":" + strconv.Itoa(version)
}

func conformanceSummaryKey(summary domain.ConformanceSummary) string {
	return summary.IntentID + ":" + strconv.Itoa(summary.IntentVersion)
}

func (s *Store) latestOperationalIntent(intentID string) (domain.OperationalIntent, bool) {
	var latest domain.OperationalIntent
	ok := false
	for _, intent := range s.operationalIntents {
		if intent.ID != intentID {
			continue
		}
		if !ok || intent.Version > latest.Version || (intent.Version == latest.Version && intent.UpdatedAt.After(latest.UpdatedAt)) {
			latest = intent
			ok = true
		}
	}
	return latest, ok
}

func (s *Store) UpsertRegulatoryAuthorization(_ context.Context, authorization domain.RegulatoryAuthorization) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizations[authorization.ID] = authorization
	return nil
}

func (s *Store) GetRegulatoryAuthorization(_ context.Context, authorizationID string) (domain.RegulatoryAuthorization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	authorization, ok := s.authorizations[authorizationID]
	if !ok {
		return domain.RegulatoryAuthorization{}, durable.ErrNotFound
	}
	return authorization, nil
}

func (s *Store) ListRegulatoryAuthorizations(_ context.Context, operatorID string) ([]domain.RegulatoryAuthorization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	authorizations := make([]domain.RegulatoryAuthorization, 0)
	for _, authorization := range s.authorizations {
		if operatorID == "" || authorization.OperatorID == operatorID {
			authorizations = append(authorizations, authorization)
		}
	}
	sort.Slice(authorizations, func(i, j int) bool {
		return authorizations[i].ValidFrom.Before(authorizations[j].ValidFrom)
	})
	return authorizations, nil
}

func (s *Store) RecordPreflightCheck(_ context.Context, check domain.PreflightCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.preflightChecks {
		if existing.ID == check.ID {
			s.preflightChecks[i] = check
			return nil
		}
	}
	s.preflightChecks = append(s.preflightChecks, check)
	return nil
}

func (s *Store) ListPreflightChecks(_ context.Context, intentID string) ([]domain.PreflightCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	checks := make([]domain.PreflightCheck, 0)
	for _, check := range s.preflightChecks {
		if intentID == "" || check.IntentID == intentID {
			checks = append(checks, check)
		}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].CapturedAt.Before(checks[j].CapturedAt) })
	return checks, nil
}

func (s *Store) CreateFlightRecord(_ context.Context, flight domain.FlightRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flightRecords[flight.ID] = flight
	return nil
}

func (s *Store) GetFlightRecord(_ context.Context, flightID string) (domain.FlightRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	flight, ok := s.flightRecords[flightID]
	if !ok {
		return domain.FlightRecord{}, durable.ErrNotFound
	}
	return flight, nil
}

func (s *Store) ListFlightRecords(_ context.Context, aircraftID string) ([]domain.FlightRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	flights := make([]domain.FlightRecord, 0)
	for _, flight := range s.flightRecords {
		if aircraftID == "" || flight.AircraftID == aircraftID {
			flights = append(flights, flight)
		}
	}
	sort.Slice(flights, func(i, j int) bool { return flights[i].StartedAt.After(flights[j].StartedAt) })
	return flights, nil
}

func (s *Store) RecordConformanceEvent(_ context.Context, event domain.ConformanceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.conformanceEvents {
		if existing.ID == event.ID {
			s.conformanceEvents[i] = event
			return nil
		}
	}
	s.conformanceEvents = append(s.conformanceEvents, event)
	return nil
}

func (s *Store) ListConformanceEvents(_ context.Context, flightID string) ([]domain.ConformanceEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]domain.ConformanceEvent, 0)
	for _, event := range s.conformanceEvents {
		if flightID == "" || event.FlightID == flightID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	return events, nil
}

func (s *Store) UpsertConformanceSummary(_ context.Context, summary domain.ConformanceSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conformanceSummaries[conformanceSummaryKey(summary)] = summary
	return nil
}

func (s *Store) GetConformanceSummary(_ context.Context, intentID string) (*domain.ConformanceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var selected *domain.ConformanceSummary
	for _, summary := range s.conformanceSummaries {
		if summary.IntentID != intentID {
			continue
		}
		candidate := summary
		if selected == nil ||
			candidate.IntentVersion > selected.IntentVersion ||
			(candidate.IntentVersion == selected.IntentVersion && candidate.UpdatedAt.After(selected.UpdatedAt)) {
			selected = &candidate
		}
	}
	return selected, nil
}

func (s *Store) ListConformanceSummaries(_ context.Context, intentID string) ([]domain.ConformanceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summaries := make([]domain.ConformanceSummary, 0)
	for _, summary := range s.conformanceSummaries {
		if intentID == "" || summary.IntentID == intentID {
			summaries = append(summaries, summary)
		}
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt) })
	return summaries, nil
}

func (s *Store) RecordEvidence(_ context.Context, record domain.EvidenceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidenceRecords[record.ID] = record
	return nil
}

func (s *Store) ListEvidence(_ context.Context, intentID string) ([]domain.EvidenceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]domain.EvidenceRecord, 0)
	for _, record := range s.evidenceRecords {
		if intentID == "" || record.IntentID == intentID {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, nil
}

func (s *Store) RecordReportabilityReview(_ context.Context, review domain.ReportabilityReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reportabilityReviews = append(s.reportabilityReviews, review)
	return nil
}

func (s *Store) ListReportabilityReviews(_ context.Context, intentID string) ([]domain.ReportabilityReview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	reviews := make([]domain.ReportabilityReview, 0)
	for _, review := range s.reportabilityReviews {
		if intentID == "" || review.IntentID == intentID {
			reviews = append(reviews, review)
		}
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].CreatedAt.After(reviews[j].CreatedAt) })
	return reviews, nil
}

func (s *Store) RecordComplianceFinding(_ context.Context, finding domain.ComplianceFinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.complianceFindings {
		if existing.ID == finding.ID {
			s.complianceFindings[i] = finding
			return nil
		}
	}
	s.complianceFindings = append(s.complianceFindings, finding)
	return nil
}

func (s *Store) ListComplianceFindings(_ context.Context, subjectType string, subjectID string) ([]domain.ComplianceFinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	findings := make([]domain.ComplianceFinding, 0)
	for _, finding := range s.complianceFindings {
		if subjectType != "" && finding.SubjectType != subjectType {
			continue
		}
		if subjectID != "" && finding.SubjectID != subjectID {
			continue
		}
		findings = append(findings, finding)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].EvaluatedAt.After(findings[j].EvaluatedAt) })
	return findings, nil
}

func (s *Store) ListComplianceFindingsForIntent(_ context.Context, intentID string) ([]domain.ComplianceFinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	findings := make([]domain.ComplianceFinding, 0)
	for _, finding := range s.complianceFindings {
		if finding.IntentID == intentID || (finding.SubjectType == "operational_intent" && finding.SubjectID == intentID) {
			findings = append(findings, finding)
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].EvaluatedAt.After(findings[j].EvaluatedAt) })
	return findings, nil
}

func (s *Store) RecordConflictFinding(_ context.Context, finding domain.ConflictFinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.conflictFindings {
		if existing.ID == finding.ID {
			s.conflictFindings[i] = finding
			return nil
		}
	}
	s.conflictFindings = append(s.conflictFindings, finding)
	return nil
}

func (s *Store) ListConflictFindings(_ context.Context, intentID string, intentVersion int) ([]domain.ConflictFinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	findings := make([]domain.ConflictFinding, 0)
	for _, finding := range s.conflictFindings {
		if intentID != "" && finding.IntentID != intentID {
			continue
		}
		if intentVersion != 0 && finding.IntentVersion != intentVersion {
			continue
		}
		findings = append(findings, finding)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].EvaluatedAt.Equal(findings[j].EvaluatedAt) {
			return findings[i].ID < findings[j].ID
		}
		return findings[i].EvaluatedAt.After(findings[j].EvaluatedAt)
	})
	return findings, nil
}

func (s *Store) ReplaceConflictFindings(_ context.Context, intentID string, intentVersion int, ruleVersion string, findings []domain.ConflictFinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, finding := range findings {
		if finding.IntentID != intentID || finding.IntentVersion != intentVersion || finding.RuleVersion != ruleVersion {
			return fmt.Errorf("conflict finding %q is outside replacement scope", finding.ID)
		}
	}
	next := make([]domain.ConflictFinding, 0, len(s.conflictFindings)+len(findings))
	for _, existing := range s.conflictFindings {
		if existing.IntentID != intentID || existing.IntentVersion != intentVersion || existing.RuleVersion != ruleVersion {
			next = append(next, existing)
		}
	}
	next = append(next, findings...)
	s.conflictFindings = next
	return nil
}

func (s *Store) UpsertOperationsPersonnel(_ context.Context, person domain.OperationsPersonnel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.personnel[person.ID] = person
	return nil
}

func (s *Store) GetOperationsPersonnel(_ context.Context, personID string) (domain.OperationsPersonnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	person, ok := s.personnel[personID]
	if !ok {
		return domain.OperationsPersonnel{}, durable.ErrNotFound
	}
	return person, nil
}

func (s *Store) RecordPersonnelAssignment(_ context.Context, assignment domain.PersonnelAssignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.personnelAssignments = append(s.personnelAssignments, assignment)
	return nil
}

func (s *Store) ListPersonnelAssignments(_ context.Context, intentID string) ([]domain.PersonnelAssignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	assignments := make([]domain.PersonnelAssignment, 0)
	for _, assignment := range s.personnelAssignments {
		if intentID == "" || assignment.IntentID == intentID {
			assignments = append(assignments, assignment)
		}
	}
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].AssignedAt.Before(assignments[j].AssignedAt) })
	return assignments, nil
}
