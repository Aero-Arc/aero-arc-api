package store

import (
	"context"
	"errors"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

var ErrNotFound = errors.New("not found")

type DurableStore interface {
	CreateAircraft(ctx context.Context, aircraft domain.Aircraft) error
	GetAircraft(ctx context.Context, aircraftID string) (domain.Aircraft, error)
	ListAircraft(ctx context.Context) ([]domain.Aircraft, error)

	CreateBattery(ctx context.Context, battery domain.Battery) error
	GetBattery(ctx context.Context, batteryID string) (domain.Battery, error)
	ListBatteries(ctx context.Context) ([]domain.Battery, error)

	RecordBatteryInstallation(ctx context.Context, installation domain.BatteryInstallation) error
	GetActiveBatteryInstallation(ctx context.Context, aircraftID string) (*domain.BatteryInstallation, error)

	RecordMaintenanceEvent(ctx context.Context, event domain.MaintenanceEvent) error
	ListMaintenanceEvents(ctx context.Context, aircraftID string) ([]domain.MaintenanceEvent, error)

	CreateFlightRecord(ctx context.Context, flight domain.FlightRecord) error
	GetFlightRecord(ctx context.Context, flightID string) (domain.FlightRecord, error)
	ListFlightRecords(ctx context.Context, aircraftID string) ([]domain.FlightRecord, error)

	RecordConformanceEvent(ctx context.Context, event domain.ConformanceEvent) error
	ListConformanceEvents(ctx context.Context, flightID string) ([]domain.ConformanceEvent, error)
}
