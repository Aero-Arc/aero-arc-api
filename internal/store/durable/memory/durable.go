package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type Store struct {
	mu                   sync.RWMutex
	aircraft             map[string]domain.Aircraft
	batteries            map[string]domain.Battery
	batteryInstallations []domain.BatteryInstallation
	operatingProfiles    map[string]domain.AircraftOperatingProfile
	operatingLimits      map[string]domain.OperatingLimit
	maintenanceEvents    []domain.MaintenanceEvent
	operationalIntents   map[string]domain.OperationalIntent
	preflightChecks      []domain.PreflightCheck
	flightRecords        map[string]domain.FlightRecord
	conformanceEvents    []domain.ConformanceEvent
	conformanceSummaries map[string]domain.ConformanceSummary
	evidenceRecords      map[string]domain.EvidenceRecord
	reportabilityReviews []domain.ReportabilityReview
	personnel            map[string]domain.OperationsPersonnel
}

func NewStore() *Store {
	return &Store{
		aircraft:             make(map[string]domain.Aircraft),
		batteries:            make(map[string]domain.Battery),
		operatingProfiles:    make(map[string]domain.AircraftOperatingProfile),
		operatingLimits:      make(map[string]domain.OperatingLimit),
		operationalIntents:   make(map[string]domain.OperationalIntent),
		flightRecords:        make(map[string]domain.FlightRecord),
		conformanceSummaries: make(map[string]domain.ConformanceSummary),
		evidenceRecords:      make(map[string]domain.EvidenceRecord),
		personnel:            make(map[string]domain.OperationsPersonnel),
	}
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
	s.operationalIntents[intent.ID] = intent
	return nil
}

func (s *Store) GetOperationalIntent(_ context.Context, intentID string) (domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.operationalIntents[intentID]
	if !ok {
		return domain.OperationalIntent{}, durable.ErrNotFound
	}
	return intent, nil
}

func (s *Store) ListOperationalIntents(_ context.Context, aircraftID string) ([]domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intents := make([]domain.OperationalIntent, 0)
	for _, intent := range s.operationalIntents {
		if aircraftID == "" || intent.AircraftID == aircraftID {
			intents = append(intents, intent)
		}
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].PlannedStartAt.Before(intents[j].PlannedStartAt) })
	return intents, nil
}

func (s *Store) RecordPreflightCheck(_ context.Context, check domain.PreflightCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.conformanceSummaries[summary.IntentID] = summary
	return nil
}

func (s *Store) GetConformanceSummary(_ context.Context, intentID string) (*domain.ConformanceSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	summary, ok := s.conformanceSummaries[intentID]
	if !ok {
		return nil, nil
	}
	return &summary, nil
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
