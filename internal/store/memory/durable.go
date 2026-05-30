package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store"
)

type DurableStore struct {
	mu                   sync.RWMutex
	aircraft             map[string]domain.Aircraft
	batteries            map[string]domain.Battery
	batteryInstallations []domain.BatteryInstallation
	maintenanceEvents    []domain.MaintenanceEvent
	flightRecords        map[string]domain.FlightRecord
	conformanceEvents    []domain.ConformanceEvent
}

func NewDurableStore() *DurableStore {
	return &DurableStore{
		aircraft:      make(map[string]domain.Aircraft),
		batteries:     make(map[string]domain.Battery),
		flightRecords: make(map[string]domain.FlightRecord),
	}
}

func (s *DurableStore) CreateAircraft(_ context.Context, aircraft domain.Aircraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aircraft[aircraft.ID] = aircraft
	return nil
}

func (s *DurableStore) GetAircraft(_ context.Context, aircraftID string) (domain.Aircraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	aircraft, ok := s.aircraft[aircraftID]
	if !ok {
		return domain.Aircraft{}, store.ErrNotFound
	}
	return aircraft, nil
}

func (s *DurableStore) ListAircraft(_ context.Context) ([]domain.Aircraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	aircraft := make([]domain.Aircraft, 0, len(s.aircraft))
	for _, item := range s.aircraft {
		aircraft = append(aircraft, item)
	}
	sort.Slice(aircraft, func(i, j int) bool { return aircraft[i].ID < aircraft[j].ID })
	return aircraft, nil
}

func (s *DurableStore) CreateBattery(_ context.Context, battery domain.Battery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batteries[battery.ID] = battery
	return nil
}

func (s *DurableStore) GetBattery(_ context.Context, batteryID string) (domain.Battery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	battery, ok := s.batteries[batteryID]
	if !ok {
		return domain.Battery{}, store.ErrNotFound
	}
	return battery, nil
}

func (s *DurableStore) ListBatteries(_ context.Context) ([]domain.Battery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batteries := make([]domain.Battery, 0, len(s.batteries))
	for _, item := range s.batteries {
		batteries = append(batteries, item)
	}
	sort.Slice(batteries, func(i, j int) bool { return batteries[i].ID < batteries[j].ID })
	return batteries, nil
}

func (s *DurableStore) RecordBatteryInstallation(_ context.Context, installation domain.BatteryInstallation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batteryInstallations = append(s.batteryInstallations, installation)
	return nil
}

func (s *DurableStore) GetActiveBatteryInstallation(_ context.Context, aircraftID string) (*domain.BatteryInstallation, error) {
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

func (s *DurableStore) RecordMaintenanceEvent(_ context.Context, event domain.MaintenanceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenanceEvents = append(s.maintenanceEvents, event)
	return nil
}

func (s *DurableStore) ListMaintenanceEvents(_ context.Context, aircraftID string) ([]domain.MaintenanceEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]domain.MaintenanceEvent, 0)
	for _, event := range s.maintenanceEvents {
		if event.AircraftID == aircraftID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OpenedAt.After(events[j].OpenedAt) })
	return events, nil
}

func (s *DurableStore) CreateFlightRecord(_ context.Context, flight domain.FlightRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flightRecords[flight.ID] = flight
	return nil
}

func (s *DurableStore) GetFlightRecord(_ context.Context, flightID string) (domain.FlightRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	flight, ok := s.flightRecords[flightID]
	if !ok {
		return domain.FlightRecord{}, store.ErrNotFound
	}
	return flight, nil
}

func (s *DurableStore) ListFlightRecords(_ context.Context, aircraftID string) ([]domain.FlightRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	flights := make([]domain.FlightRecord, 0)
	for _, flight := range s.flightRecords {
		if flight.AircraftID == aircraftID {
			flights = append(flights, flight)
		}
	}
	sort.Slice(flights, func(i, j int) bool { return flights[i].StartedAt.After(flights[j].StartedAt) })
	return flights, nil
}

func (s *DurableStore) RecordConformanceEvent(_ context.Context, event domain.ConformanceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conformanceEvents = append(s.conformanceEvents, event)
	return nil
}

func (s *DurableStore) ListConformanceEvents(_ context.Context, flightID string) ([]domain.ConformanceEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]domain.ConformanceEvent, 0)
	for _, event := range s.conformanceEvents {
		if event.FlightID == flightID {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	return events, nil
}
