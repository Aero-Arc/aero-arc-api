// Package durable owns authoritative operational records that should survive
// process restarts: aircraft, batteries, installations, maintenance events,
// flight records, and conformance events.
//
// Implementations should be backed by a transactional durable database such as
// Postgres or TiDB in production. They should not own high-frequency telemetry
// samples or raw replay object storage.
package durable

import (
	"context"
	"errors"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrVersionConflict = errors.New("version conflict")
)

type CandidateQuery struct {
	ExcludeIntentID string
	Volumes         []domain.OperationalVolume
}

type Candidate struct {
	IntentID      string
	IntentVersion int
}

// OperationalStore is the durable boundary for the operational-intent and
// deconfliction vertical slice. Production implementations must keep these
// records and the spatial candidate index in one shared transactional store.
type OperationalStore interface {
	FindCandidates(context.Context, CandidateQuery) ([]Candidate, error)
	CreateOperationalIntent(ctx context.Context, intent domain.OperationalIntent) error
	UpdateOperationalIntent(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64) error
	AcceptOperationalIntent(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64) error
	GetOperationalIntent(ctx context.Context, intentID string) (domain.OperationalIntent, error)
	GetOperationalIntentVersion(ctx context.Context, intentID string, version int) (domain.OperationalIntent, error)
	ListOperationalIntents(ctx context.Context, aircraftID string) ([]domain.OperationalIntent, error)
	ListOperationalIntentVersions(ctx context.Context, intentID string) ([]domain.OperationalIntent, error)
	RecordOperationalVolume(ctx context.Context, volume domain.OperationalVolume) error
	ReplaceOperationalVolumes(ctx context.Context, intentID string, intentVersion int, volumes []domain.OperationalVolume) error
	ReplaceOperationalIntent(ctx context.Context, expectedVersion int, expectedRevision int64, intent domain.OperationalIntent, volumes []domain.OperationalVolume) error
	ListOperationalVolumes(ctx context.Context, intentID string) ([]domain.OperationalVolume, error)
	RecordConflictFinding(ctx context.Context, finding domain.ConflictFinding) error
	ListConflictFindings(ctx context.Context, intentID string, intentVersion int) ([]domain.ConflictFinding, error)
	ReplaceConflictFindings(ctx context.Context, intentID string, intentVersion int, ruleVersion string, findings []domain.ConflictFinding) error
}

// CoordinationStore persists DSS desired state and leases pending work. The
// lifecycle-and-publication methods must commit both changes atomically.
type CoordinationStore interface {
	AcceptOperationalIntentAndRequestPublication(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64, publication domain.OperationalIntentPublication) error
	UpdateOperationalIntentAndRequestPublication(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64, publication domain.OperationalIntentPublication) error
	RequestOperationalIntentPublication(ctx context.Context, publication domain.OperationalIntentPublication) error
	GetOperationalIntentPublication(ctx context.Context, intentID string) (domain.OperationalIntentPublication, error)
	ClaimOperationalIntentPublication(ctx context.Context, intentID string, now, leaseUntil time.Time) (domain.OperationalIntentPublication, error)
	ClaimDueOperationalIntentPublications(ctx context.Context, now, leaseUntil time.Time, limit int) ([]domain.OperationalIntentPublication, error)
	UpdateOperationalIntentPublication(ctx context.Context, publication domain.OperationalIntentPublication, expectedRevision int64) error
	ConfirmOperationalIntentPublication(ctx context.Context, publication domain.OperationalIntentPublication, expectedRevision int64, notifications []domain.PeerNotification) error
	ClaimDuePeerNotifications(ctx context.Context, now, leaseUntil time.Time, limit int) ([]domain.PeerNotification, error)
	UpdatePeerNotification(ctx context.Context, notification domain.PeerNotification, expectedRevision int64) error
	RecordReceivedPeerNotification(ctx context.Context, notification domain.ReceivedPeerNotification) error
}

// Store defines the durable system-of-record operations used by the API.
type Store interface {
	OperationalStore
	CoordinationStore
	UpsertOperator(ctx context.Context, operator domain.Operator) error
	GetOperator(ctx context.Context, operatorID string) (domain.Operator, error)
	ListOperators(ctx context.Context) ([]domain.Operator, error)

	CreateAircraft(ctx context.Context, aircraft domain.Aircraft) error
	GetAircraft(ctx context.Context, aircraftID string) (domain.Aircraft, error)
	ListAircraft(ctx context.Context) ([]domain.Aircraft, error)

	CreateBattery(ctx context.Context, battery domain.Battery) error
	GetBattery(ctx context.Context, batteryID string) (domain.Battery, error)
	ListBatteries(ctx context.Context) ([]domain.Battery, error)

	RecordBatteryInstallation(ctx context.Context, installation domain.BatteryInstallation) error
	GetActiveBatteryInstallation(ctx context.Context, aircraftID string) (*domain.BatteryInstallation, error)

	UpsertAircraftOperatingProfile(ctx context.Context, profile domain.AircraftOperatingProfile) error
	GetAircraftOperatingProfile(ctx context.Context, aircraftID string) (*domain.AircraftOperatingProfile, error)
	ListOperatingLimits(ctx context.Context, aircraftID string) ([]domain.OperatingLimit, error)
	UpsertOperatingLimit(ctx context.Context, limit domain.OperatingLimit) error

	RecordMaintenanceEvent(ctx context.Context, event domain.MaintenanceEvent) error
	ListMaintenanceEvents(ctx context.Context, aircraftID string) ([]domain.MaintenanceEvent, error)

	UpsertRegulatoryAuthorization(ctx context.Context, authorization domain.RegulatoryAuthorization) error
	GetRegulatoryAuthorization(ctx context.Context, authorizationID string) (domain.RegulatoryAuthorization, error)
	ListRegulatoryAuthorizations(ctx context.Context, operatorID string) ([]domain.RegulatoryAuthorization, error)

	RecordPreflightCheck(ctx context.Context, check domain.PreflightCheck) error
	ListPreflightChecks(ctx context.Context, intentID string) ([]domain.PreflightCheck, error)

	CreateFlightRecord(ctx context.Context, flight domain.FlightRecord) error
	GetFlightRecord(ctx context.Context, flightID string) (domain.FlightRecord, error)
	ListFlightRecords(ctx context.Context, aircraftID string) ([]domain.FlightRecord, error)

	RecordConformanceEvent(ctx context.Context, event domain.ConformanceEvent) error
	ListConformanceEvents(ctx context.Context, flightID string) ([]domain.ConformanceEvent, error)
	UpsertConformanceSummary(ctx context.Context, summary domain.ConformanceSummary) error
	GetConformanceSummary(ctx context.Context, intentID string) (*domain.ConformanceSummary, error)
	ListConformanceSummaries(ctx context.Context, intentID string) ([]domain.ConformanceSummary, error)

	RecordEvidence(ctx context.Context, record domain.EvidenceRecord) error
	ListEvidence(ctx context.Context, intentID string) ([]domain.EvidenceRecord, error)

	RecordReportabilityReview(ctx context.Context, review domain.ReportabilityReview) error
	ListReportabilityReviews(ctx context.Context, intentID string) ([]domain.ReportabilityReview, error)
	RecordComplianceFinding(ctx context.Context, finding domain.ComplianceFinding) error
	ListComplianceFindings(ctx context.Context, subjectType string, subjectID string) ([]domain.ComplianceFinding, error)
	ListComplianceFindingsForIntent(ctx context.Context, intentID string) ([]domain.ComplianceFinding, error)
	UpsertOperationsPersonnel(ctx context.Context, person domain.OperationsPersonnel) error
	GetOperationsPersonnel(ctx context.Context, personID string) (domain.OperationsPersonnel, error)
	RecordPersonnelAssignment(ctx context.Context, assignment domain.PersonnelAssignment) error
	ListPersonnelAssignments(ctx context.Context, intentID string) ([]domain.PersonnelAssignment, error)
}
