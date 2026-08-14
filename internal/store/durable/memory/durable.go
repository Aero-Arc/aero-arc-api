package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type Store struct {
	mu                        sync.RWMutex
	operators                 map[string]domain.Operator
	aircraft                  map[string]domain.Aircraft
	batteries                 map[string]domain.Battery
	batteryInstallations      []domain.BatteryInstallation
	operatingProfiles         map[string]domain.AircraftOperatingProfile
	operatingLimits           map[string]domain.OperatingLimit
	maintenanceEvents         []domain.MaintenanceEvent
	operationalIntents        map[string]domain.OperationalIntent
	operationalVolumes        map[string]domain.OperationalVolume
	authorizations            map[string]domain.RegulatoryAuthorization
	preflightChecks           []domain.PreflightCheck
	flightRecords             map[string]domain.FlightRecord
	conformanceEvents         []domain.ConformanceEvent
	conformanceSummaries      map[string]domain.ConformanceSummary
	evidenceRecords           map[string]domain.EvidenceRecord
	reportabilityReviews      []domain.ReportabilityReview
	complianceFindings        []domain.ComplianceFinding
	conflictFindings          []domain.ConflictFinding
	personnel                 map[string]domain.OperationsPersonnel
	personnelAssignments      []domain.PersonnelAssignment
	publications              map[string]domain.OperationalIntentPublication
	peerNotifications         map[string]domain.PeerNotification
	receivedPeerNotifications map[string]domain.ReceivedPeerNotification
}

var _ durable.OperationalStore = (*Store)(nil)

// NewStore constructs memory from the supplied configuration and dependencies.
//
// Returns:
//   - result: is the *Store value produced by NewStore.
func NewStore() *Store {
	return &Store{
		operators:                 make(map[string]domain.Operator),
		aircraft:                  make(map[string]domain.Aircraft),
		batteries:                 make(map[string]domain.Battery),
		operatingProfiles:         make(map[string]domain.AircraftOperatingProfile),
		operatingLimits:           make(map[string]domain.OperatingLimit),
		operationalIntents:        make(map[string]domain.OperationalIntent),
		operationalVolumes:        make(map[string]domain.OperationalVolume),
		authorizations:            make(map[string]domain.RegulatoryAuthorization),
		flightRecords:             make(map[string]domain.FlightRecord),
		conformanceSummaries:      make(map[string]domain.ConformanceSummary),
		evidenceRecords:           make(map[string]domain.EvidenceRecord),
		personnel:                 make(map[string]domain.OperationsPersonnel),
		publications:              make(map[string]domain.OperationalIntentPublication),
		peerNotifications:         make(map[string]domain.PeerNotification),
		receivedPeerNotifications: make(map[string]domain.ReceivedPeerNotification),
	}
}

// UpsertOperator creates or replaces the supplied Store record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to UpsertOperator.
//   - operator: is the domain.Operator value supplied to UpsertOperator.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpsertOperator(_ context.Context, operator domain.Operator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operators[operator.ID] = operator
	return nil
}

// GetOperator returns one operator by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to GetOperator.
//   - operatorID: identifies the target operator.
//
// Returns:
//   - result: is the domain.Operator value produced by GetOperator.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperator(_ context.Context, operatorID string) (domain.Operator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operator, ok := s.operators[operatorID]
	if !ok {
		return domain.Operator{}, durable.ErrNotFound
	}
	return operator, nil
}

// ListOperators returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListOperators.
//
// Returns:
//   - result: is the []domain.Operator value produced by ListOperators.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// CreateAircraft creates and stores the supplied Store record.
//
// Parameters:
//   - value: is the context.Context value supplied to CreateAircraft.
//   - aircraft: is the domain.Aircraft value supplied to CreateAircraft.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) CreateAircraft(_ context.Context, aircraft domain.Aircraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aircraft[aircraft.ID] = aircraft
	return nil
}

// GetAircraft returns one aircraft by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to GetAircraft.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the domain.Aircraft value produced by GetAircraft.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetAircraft(_ context.Context, aircraftID string) (domain.Aircraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	aircraft, ok := s.aircraft[aircraftID]
	if !ok {
		return domain.Aircraft{}, durable.ErrNotFound
	}
	return aircraft, nil
}

// ListAircraft returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListAircraft.
//
// Returns:
//   - result: is the []domain.Aircraft value produced by ListAircraft.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// CreateBattery creates and stores the supplied Store record.
//
// Parameters:
//   - value: is the context.Context value supplied to CreateBattery.
//   - battery: is the domain.Battery value supplied to CreateBattery.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) CreateBattery(_ context.Context, battery domain.Battery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batteries[battery.ID] = battery
	return nil
}

// GetBattery returns one battery by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to GetBattery.
//   - batteryID: identifies the target battery.
//
// Returns:
//   - result: is the domain.Battery value produced by GetBattery.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetBattery(_ context.Context, batteryID string) (domain.Battery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	battery, ok := s.batteries[batteryID]
	if !ok {
		return domain.Battery{}, durable.ErrNotFound
	}
	return battery, nil
}

// ListBatteries returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListBatteries.
//
// Returns:
//   - result: is the []domain.Battery value produced by ListBatteries.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordBatteryInstallation durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordBatteryInstallation.
//   - installation: is the domain.BatteryInstallation value supplied to RecordBatteryInstallation.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordBatteryInstallation(_ context.Context, installation domain.BatteryInstallation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batteryInstallations = append(s.batteryInstallations, installation)
	return nil
}

// GetActiveBatteryInstallation returns the current battery installation for an aircraft.
//
// Parameters:
//   - value: is the context.Context value supplied to GetActiveBatteryInstallation.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the *domain.BatteryInstallation value produced by GetActiveBatteryInstallation.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// UpsertAircraftOperatingProfile creates or replaces the supplied Store record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to UpsertAircraftOperatingProfile.
//   - profile: is the domain.AircraftOperatingProfile value supplied to UpsertAircraftOperatingProfile.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpsertAircraftOperatingProfile(_ context.Context, profile domain.AircraftOperatingProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatingProfiles[profile.AircraftID] = profile
	return nil
}

// GetAircraftOperatingProfile returns the operating profile assigned to an aircraft.
//
// Parameters:
//   - value: is the context.Context value supplied to GetAircraftOperatingProfile.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the *domain.AircraftOperatingProfile value produced by GetAircraftOperatingProfile.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetAircraftOperatingProfile(_ context.Context, aircraftID string) (*domain.AircraftOperatingProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.operatingProfiles[aircraftID]
	if !ok {
		return nil, nil
	}
	return &profile, nil
}

// ListOperatingLimits returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListOperatingLimits.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the []domain.OperatingLimit value produced by ListOperatingLimits.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// UpsertOperatingLimit creates or replaces the supplied Store record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to UpsertOperatingLimit.
//   - limit: caps the number of records claimed or returned in one call.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpsertOperatingLimit(_ context.Context, limit domain.OperatingLimit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatingLimits[limit.ID] = limit
	return nil
}

// RecordMaintenanceEvent durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordMaintenanceEvent.
//   - event: is the domain.MaintenanceEvent value supplied to RecordMaintenanceEvent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordMaintenanceEvent(_ context.Context, event domain.MaintenanceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenanceEvents = append(s.maintenanceEvents, event)
	return nil
}

// ListMaintenanceEvents returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListMaintenanceEvents.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the []domain.MaintenanceEvent value produced by ListMaintenanceEvents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// CreateOperationalIntent creates and stores the supplied Store record.
//
// Parameters:
//   - value: is the context.Context value supplied to CreateOperationalIntent.
//   - intent: is the domain.OperationalIntent value supplied to CreateOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// UpdateOperationalIntent updates the selected Store state while enforcing its consistency checks.
//
// Parameters:
//   - value: is the context.Context value supplied to UpdateOperationalIntent.
//   - intent: is the domain.OperationalIntent value supplied to UpdateOperationalIntent.
//   - expectedRevision: is the int64 value supplied to UpdateOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpdateOperationalIntent(_ context.Context, intent domain.OperationalIntent, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateOperationalIntentLocked(intent, expectedRevision)
}

func (s *Store) updateOperationalIntentLocked(intent domain.OperationalIntent, expectedRevision int64) error {
	current, exists := s.latestOperationalIntent(intent.ID)
	if !exists {
		return durable.ErrNotFound
	}
	if current.Version != intent.Version || current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	intent.Revision = expectedRevision + 1
	s.operationalIntents[operationalIntentKey(intent.ID, intent.Version)] = intent
	if intent.Status == domain.IntentStatusCanceled || intent.Status == domain.IntentStatusComplete {
		for key, prior := range s.operationalIntents {
			if prior.ID != intent.ID || prior.Version >= intent.Version || prior.Status != domain.IntentStatusAccepted {
				continue
			}
			prior.Status = intent.Status
			prior.CanceledAt = intent.CanceledAt
			prior.CompletedAt = intent.CompletedAt
			prior.UpdatedAt = intent.UpdatedAt
			prior.Revision++
			s.operationalIntents[key] = prior
		}
	}
	return nil
}

// AcceptOperationalIntent accepts the selected Store state after validating its current revision.
//
// Parameters:
//   - value: is the context.Context value supplied to AcceptOperationalIntent.
//   - intent: is the domain.OperationalIntent value supplied to AcceptOperationalIntent.
//   - expectedRevision: is the int64 value supplied to AcceptOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) AcceptOperationalIntent(_ context.Context, intent domain.OperationalIntent, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptOperationalIntentLocked(intent, expectedRevision)
}

func (s *Store) acceptOperationalIntentLocked(intent domain.OperationalIntent, expectedRevision int64) error {
	current, exists := s.latestOperationalIntent(intent.ID)
	if !exists {
		return durable.ErrNotFound
	}
	if current.Version != intent.Version || current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	intent.Revision = expectedRevision + 1
	s.operationalIntents[operationalIntentKey(intent.ID, intent.Version)] = intent
	for key, prior := range s.operationalIntents {
		if prior.ID != intent.ID || prior.Version >= intent.Version || prior.Status != domain.IntentStatusAccepted {
			continue
		}
		prior.Status = domain.IntentStatusSuperseded
		prior.SupersededAt = intent.AcceptedAt
		prior.UpdatedAt = intent.UpdatedAt
		prior.Revision++
		s.operationalIntents[key] = prior
	}
	return nil
}

// AcceptOperationalIntentAndRequestPublication accepts the selected Store state after validating its current revision.
//
// Parameters:
//   - value: is the context.Context value supplied to AcceptOperationalIntentAndRequestPublication.
//   - intent: is the domain.OperationalIntent value supplied to AcceptOperationalIntentAndRequestPublication.
//   - expectedRevision: is the int64 value supplied to AcceptOperationalIntentAndRequestPublication.
//   - publication: is the domain.OperationalIntentPublication value supplied to AcceptOperationalIntentAndRequestPublication.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) AcceptOperationalIntentAndRequestPublication(_ context.Context, intent domain.OperationalIntent, expectedRevision int64, publication domain.OperationalIntentPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.acceptOperationalIntentLocked(intent, expectedRevision); err != nil {
		return err
	}
	s.requestPublicationLocked(publication)
	return nil
}

// UpdateOperationalIntentAndRequestPublication updates the selected Store state while enforcing its consistency checks.
//
// Parameters:
//   - value: is the context.Context value supplied to UpdateOperationalIntentAndRequestPublication.
//   - intent: is the domain.OperationalIntent value supplied to UpdateOperationalIntentAndRequestPublication.
//   - expectedRevision: is the int64 value supplied to UpdateOperationalIntentAndRequestPublication.
//   - publication: is the domain.OperationalIntentPublication value supplied to UpdateOperationalIntentAndRequestPublication.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpdateOperationalIntentAndRequestPublication(_ context.Context, intent domain.OperationalIntent, expectedRevision int64, publication domain.OperationalIntentPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.updateOperationalIntentLocked(intent, expectedRevision); err != nil {
		return err
	}
	s.requestPublicationLocked(publication)
	return nil
}

// RequestOperationalIntentPublication requests the selected Store operation and records it for processing.
//
// Parameters:
//   - value: is the context.Context value supplied to RequestOperationalIntentPublication.
//   - publication: is the domain.OperationalIntentPublication value supplied to RequestOperationalIntentPublication.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RequestOperationalIntentPublication(_ context.Context, publication domain.OperationalIntentPublication) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.operationalIntents[operationalIntentKey(publication.IntentID, publication.DesiredIntentVersion)]; !ok {
		return durable.ErrNotFound
	}
	s.requestPublicationLocked(publication)
	return nil
}

// RequestOperationalIntentPublicationIfCurrent requests the selected Store operation and records it for processing.
//
// Parameters:
//   - value: is the context.Context value supplied to RequestOperationalIntentPublicationIfCurrent.
//   - publication: is the domain.OperationalIntentPublication value supplied to RequestOperationalIntentPublicationIfCurrent.
//   - expectedIntentVersion: is the int value supplied to RequestOperationalIntentPublicationIfCurrent.
//   - expectedIntentRevision: is the int64 value supplied to RequestOperationalIntentPublicationIfCurrent.
//   - expectedStatus: is the domain.IntentStatus value supplied to RequestOperationalIntentPublicationIfCurrent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RequestOperationalIntentPublicationIfCurrent(_ context.Context, publication domain.OperationalIntentPublication, expectedIntentVersion int, expectedIntentRevision int64, expectedStatus domain.IntentStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.latestOperationalIntent(publication.IntentID)
	if !ok {
		return durable.ErrNotFound
	}
	if current.Version != expectedIntentVersion || current.Revision != expectedIntentRevision || current.Status != expectedStatus {
		return durable.ErrVersionConflict
	}
	if _, ok := s.operationalIntents[operationalIntentKey(publication.IntentID, publication.DesiredIntentVersion)]; !ok {
		return durable.ErrNotFound
	}
	s.requestPublicationLocked(publication)
	return nil
}

func (s *Store) requestPublicationLocked(request domain.OperationalIntentPublication) {
	current, exists := s.publications[request.IntentID]
	request.LeaseUntil = nil
	if exists {
		request.Revision = current.Revision + 1
		request.PublishedIntentVersion = current.PublishedIntentVersion
		request.ConfirmedState = current.ConfirmedState
		request.DSSVersion = current.DSSVersion
		request.OVN = current.OVN
		request.SubscriptionID = current.SubscriptionID
		request.Manager = current.Manager
		request.USSBaseURL = current.USSBaseURL
		request.ReferenceJSON = append([]byte(nil), current.ReferenceJSON...)
		request.ConfirmedAt = current.ConfirmedAt
		request.LeaseUntil = current.LeaseUntil
		request.LastAttemptAt = current.LastAttemptAt
		if current.SyncStatus == domain.PublicationSyncWithdrawn && request.DesiredState != domain.OperationalIntentExternalStateWithdrawn {
			request.PublishedIntentVersion = 0
			request.DSSVersion = 0
			request.OVN = ""
			request.ReferenceJSON = nil
			request.ConfirmedAt = nil
		}
	}
	request.SyncStatus = domain.PublicationSyncPending
	request.AttemptCount = 0
	request.LastError = ""
	s.publications[request.IntentID] = clonePublication(request)
}

// GetOperationalIntentPublication returns the current DSS publication state for an intent.
//
// Parameters:
//   - value: is the context.Context value supplied to GetOperationalIntentPublication.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the domain.OperationalIntentPublication value produced by GetOperationalIntentPublication.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperationalIntentPublication(_ context.Context, intentID string) (domain.OperationalIntentPublication, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	publication, ok := s.publications[intentID]
	if !ok {
		return domain.OperationalIntentPublication{}, durable.ErrNotFound
	}
	return clonePublication(publication), nil
}

// ClaimOperationalIntentPublication atomically leases eligible Store work to a worker.
//
// Parameters:
//   - value: is the context.Context value supplied to ClaimOperationalIntentPublication.
//   - intentID: identifies the target intent.
//   - now: supplies the event or wall-clock timestamp used by the operation.
//   - leaseUntil: is the time.Time value supplied to ClaimOperationalIntentPublication.
//
// Returns:
//   - result: is the domain.OperationalIntentPublication value produced by ClaimOperationalIntentPublication.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) ClaimOperationalIntentPublication(_ context.Context, intentID string, now, leaseUntil time.Time) (domain.OperationalIntentPublication, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	publication, ok := s.publications[intentID]
	if !ok {
		return domain.OperationalIntentPublication{}, durable.ErrNotFound
	}
	if publication.LeaseUntil != nil && publication.LeaseUntil.After(now) {
		return domain.OperationalIntentPublication{}, durable.ErrVersionConflict
	}
	publication.Revision++
	publication.SyncStatus = domain.PublicationSyncProcessing
	publication.LeaseUntil = &leaseUntil
	publication.LastAttemptAt = &now
	publication.AttemptCount++
	publication.UpdatedAt = now
	s.publications[intentID] = clonePublication(publication)
	return clonePublication(publication), nil
}

// ClaimDueOperationalIntentPublications atomically leases eligible Store work to a worker.
//
// Parameters:
//   - value: is the context.Context value supplied to ClaimDueOperationalIntentPublications.
//   - now: supplies the event or wall-clock timestamp used by the operation.
//   - leaseUntil: is the time.Time value supplied to ClaimDueOperationalIntentPublications.
//   - limit: caps the number of records claimed or returned in one call.
//
// Returns:
//   - result: is the []domain.OperationalIntentPublication value produced by ClaimDueOperationalIntentPublications.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) ClaimDueOperationalIntentPublications(_ context.Context, now, leaseUntil time.Time, limit int) ([]domain.OperationalIntentPublication, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		return []domain.OperationalIntentPublication{}, nil
	}
	ids := make([]string, 0, len(s.publications))
	for id, publication := range s.publications {
		if publication.NextAttemptAt.After(now) || publication.LeaseUntil != nil && publication.LeaseUntil.After(now) {
			continue
		}
		if publication.SyncStatus != domain.PublicationSyncPending &&
			publication.SyncStatus != domain.PublicationSyncRetrying &&
			publication.SyncStatus != domain.PublicationSyncProcessing {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	claimed := make([]domain.OperationalIntentPublication, 0, len(ids))
	for _, id := range ids {
		publication := s.publications[id]
		publication.Revision++
		publication.SyncStatus = domain.PublicationSyncProcessing
		publication.LeaseUntil = &leaseUntil
		publication.LastAttemptAt = &now
		publication.AttemptCount++
		publication.UpdatedAt = now
		s.publications[id] = clonePublication(publication)
		claimed = append(claimed, clonePublication(publication))
	}
	return claimed, nil
}

// RenewOperationalIntentPublicationLease extends the selected Store lease when the caller still owns its fence.
//
// Parameters:
//   - value: is the context.Context value supplied to RenewOperationalIntentPublicationLease.
//   - intentID: identifies the target intent.
//   - expectedRevision: is the int64 value supplied to RenewOperationalIntentPublicationLease.
//   - leaseUntil: is the time.Time value supplied to RenewOperationalIntentPublicationLease.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RenewOperationalIntentPublicationLease(_ context.Context, intentID string, expectedRevision int64, leaseUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	publication, ok := s.publications[intentID]
	if !ok {
		return durable.ErrNotFound
	}
	if publication.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	publication.LeaseUntil = &leaseUntil
	s.publications[intentID] = clonePublication(publication)
	return nil
}

// UpdateOperationalIntentPublication updates the selected Store state while enforcing its consistency checks.
//
// Parameters:
//   - value: is the context.Context value supplied to UpdateOperationalIntentPublication.
//   - publication: is the domain.OperationalIntentPublication value supplied to UpdateOperationalIntentPublication.
//   - expectedRevision: is the int64 value supplied to UpdateOperationalIntentPublication.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpdateOperationalIntentPublication(_ context.Context, publication domain.OperationalIntentPublication, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.publications[publication.IntentID]
	if !ok {
		return durable.ErrNotFound
	}
	if current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	publication.Revision = expectedRevision + 1
	publication.LeaseUntil = nil
	s.publications[publication.IntentID] = clonePublication(publication)
	return nil
}

// ConfirmOperationalIntentPublication confirms the selected Store transition and records its durable outcome.
//
// Parameters:
//   - value: is the context.Context value supplied to ConfirmOperationalIntentPublication.
//   - publication: is the domain.OperationalIntentPublication value supplied to ConfirmOperationalIntentPublication.
//   - expectedRevision: is the int64 value supplied to ConfirmOperationalIntentPublication.
//   - notifications: is the []domain.PeerNotification value supplied to ConfirmOperationalIntentPublication.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) ConfirmOperationalIntentPublication(_ context.Context, publication domain.OperationalIntentPublication, expectedRevision int64, notifications []domain.PeerNotification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.publications[publication.IntentID]
	if !ok {
		return durable.ErrNotFound
	}
	if current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	publication.Revision = expectedRevision + 1
	publication.LeaseUntil = nil
	s.publications[publication.IntentID] = clonePublication(publication)
	s.enqueuePeerNotificationsLocked(notifications)
	return nil
}

func (s *Store) enqueuePeerNotificationsLocked(notifications []domain.PeerNotification) {
	for _, notification := range notifications {
		if _, exists := s.peerNotifications[notification.ID]; exists {
			continue
		}
		copy := notification
		copy.Revision = 0
		copy.Payload = append([]byte(nil), notification.Payload...)
		s.peerNotifications[notification.ID] = copy
	}
}

// ClaimDuePeerNotifications atomically leases eligible Store work to a worker.
//
// Parameters:
//   - value: is the context.Context value supplied to ClaimDuePeerNotifications.
//   - now: supplies the event or wall-clock timestamp used by the operation.
//   - leaseUntil: is the time.Time value supplied to ClaimDuePeerNotifications.
//   - limit: caps the number of records claimed or returned in one call.
//
// Returns:
//   - result: is the []domain.PeerNotification value produced by ClaimDuePeerNotifications.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) ClaimDuePeerNotifications(_ context.Context, now, leaseUntil time.Time, limit int) ([]domain.PeerNotification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0)
	for id, notification := range s.peerNotifications {
		if notification.DeliveredAt == nil && !notification.NextAttemptAt.After(now) && (notification.LeaseUntil == nil || !notification.LeaseUntil.After(now)) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if limit < len(ids) {
		ids = ids[:max(limit, 0)]
	}
	claimed := make([]domain.PeerNotification, 0, len(ids))
	for _, id := range ids {
		notification := s.peerNotifications[id]
		notification.Revision++
		notification.LeaseUntil = &leaseUntil
		notification.AttemptCount++
		notification.UpdatedAt = now
		s.peerNotifications[id] = notification
		claimed = append(claimed, notification)
	}
	return claimed, nil
}

// UpdatePeerNotification updates the selected Store state while enforcing its consistency checks.
//
// Parameters:
//   - value: is the context.Context value supplied to UpdatePeerNotification.
//   - notification: is the domain.PeerNotification value supplied to UpdatePeerNotification.
//   - expectedRevision: is the int64 value supplied to UpdatePeerNotification.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpdatePeerNotification(_ context.Context, notification domain.PeerNotification, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.peerNotifications[notification.ID]
	if !ok {
		return durable.ErrNotFound
	}
	if current.Revision != expectedRevision {
		return durable.ErrVersionConflict
	}
	notification.Revision = expectedRevision + 1
	notification.LeaseUntil = nil
	s.peerNotifications[notification.ID] = notification
	return nil
}

// RecordReceivedPeerNotification durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordReceivedPeerNotification.
//   - notification: is the domain.ReceivedPeerNotification value supplied to RecordReceivedPeerNotification.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordReceivedPeerNotification(_ context.Context, notification domain.ReceivedPeerNotification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := notification
	copy.Payload = append([]byte(nil), notification.Payload...)
	s.receivedPeerNotifications[notification.ID] = copy
	return nil
}

// ListReceivedPeerNotifications returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListReceivedPeerNotifications.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.ReceivedPeerNotification value produced by ListReceivedPeerNotifications.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) ListReceivedPeerNotifications(_ context.Context, intentID string) ([]domain.ReceivedPeerNotification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	notifications := make([]domain.ReceivedPeerNotification, 0)
	for _, notification := range s.receivedPeerNotifications {
		if intentID == "" || notification.IntentID == intentID {
			copy := notification
			copy.Payload = append([]byte(nil), notification.Payload...)
			notifications = append(notifications, copy)
		}
	}
	sort.Slice(notifications, func(i, j int) bool { return notifications[i].ReceivedAt.Before(notifications[j].ReceivedAt) })
	return notifications, nil
}

func clonePublication(publication domain.OperationalIntentPublication) domain.OperationalIntentPublication {
	publication.ReferenceJSON = append([]byte(nil), publication.ReferenceJSON...)
	return publication
}

// GetOperationalIntent returns the current version of one operational intent.
//
// Parameters:
//   - value: is the context.Context value supplied to GetOperationalIntent.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the domain.OperationalIntent value produced by GetOperationalIntent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperationalIntent(_ context.Context, intentID string) (domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.latestOperationalIntent(intentID)
	if !ok {
		return domain.OperationalIntent{}, durable.ErrNotFound
	}
	return intent, nil
}

// GetOperationalIntentVersion returns one immutable historical intent version.
//
// Parameters:
//   - value: is the context.Context value supplied to GetOperationalIntentVersion.
//   - intentID: identifies the target intent.
//   - version: is the int value supplied to GetOperationalIntentVersion.
//
// Returns:
//   - result: is the domain.OperationalIntent value produced by GetOperationalIntentVersion.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperationalIntentVersion(_ context.Context, intentID string, version int) (domain.OperationalIntent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	intent, ok := s.operationalIntents[operationalIntentKey(intentID, version)]
	if !ok {
		return domain.OperationalIntent{}, durable.ErrNotFound
	}
	return intent, nil
}

// ListOperationalIntents returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListOperationalIntents.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the []domain.OperationalIntent value produced by ListOperationalIntents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListOperationalIntentVersions returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListOperationalIntentVersions.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.OperationalIntent value produced by ListOperationalIntentVersions.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordOperationalVolume durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordOperationalVolume.
//   - volume: is the domain.OperationalVolume value supplied to RecordOperationalVolume.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordOperationalVolume(_ context.Context, volume domain.OperationalVolume) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operationalVolumes[operationalVolumeKey(volume)] = volume
	return nil
}

// ReplaceOperationalVolumes atomically replaces the selected Store records.
//
// Parameters:
//   - value: is the context.Context value supplied to ReplaceOperationalVolumes.
//   - intentID: identifies the target intent.
//   - intentVersion: is the int value supplied to ReplaceOperationalVolumes.
//   - volumes: is the []domain.OperationalVolume value supplied to ReplaceOperationalVolumes.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ReplaceOperationalIntent atomically replaces the selected Store records.
//
// Parameters:
//   - value: is the context.Context value supplied to ReplaceOperationalIntent.
//   - expectedVersion: is the int value supplied to ReplaceOperationalIntent.
//   - expectedRevision: is the int64 value supplied to ReplaceOperationalIntent.
//   - intent: is the domain.OperationalIntent value supplied to ReplaceOperationalIntent.
//   - volumes: is the []domain.OperationalVolume value supplied to ReplaceOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListOperationalVolumes returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListOperationalVolumes.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.OperationalVolume value produced by ListOperationalVolumes.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// UpsertRegulatoryAuthorization creates or replaces the supplied Store record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to UpsertRegulatoryAuthorization.
//   - authorization: is the domain.RegulatoryAuthorization value supplied to UpsertRegulatoryAuthorization.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpsertRegulatoryAuthorization(_ context.Context, authorization domain.RegulatoryAuthorization) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizations[authorization.ID] = authorization
	return nil
}

// GetRegulatoryAuthorization returns one regulatory authorization by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to GetRegulatoryAuthorization.
//   - authorizationID: identifies the target authorization.
//
// Returns:
//   - result: is the domain.RegulatoryAuthorization value produced by GetRegulatoryAuthorization.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetRegulatoryAuthorization(_ context.Context, authorizationID string) (domain.RegulatoryAuthorization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	authorization, ok := s.authorizations[authorizationID]
	if !ok {
		return domain.RegulatoryAuthorization{}, durable.ErrNotFound
	}
	return authorization, nil
}

// ListRegulatoryAuthorizations returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListRegulatoryAuthorizations.
//   - operatorID: identifies the target operator.
//
// Returns:
//   - result: is the []domain.RegulatoryAuthorization value produced by ListRegulatoryAuthorizations.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordPreflightCheck durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordPreflightCheck.
//   - check: is the domain.PreflightCheck value supplied to RecordPreflightCheck.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListPreflightChecks returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListPreflightChecks.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.PreflightCheck value produced by ListPreflightChecks.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// CreateFlightRecord creates and stores the supplied Store record.
//
// Parameters:
//   - value: is the context.Context value supplied to CreateFlightRecord.
//   - flight: is the domain.FlightRecord value supplied to CreateFlightRecord.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) CreateFlightRecord(_ context.Context, flight domain.FlightRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flightRecords[flight.ID] = flight
	return nil
}

// GetFlightRecord returns one durable flight record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to GetFlightRecord.
//   - flightID: identifies the target flight.
//
// Returns:
//   - result: is the domain.FlightRecord value produced by GetFlightRecord.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetFlightRecord(_ context.Context, flightID string) (domain.FlightRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	flight, ok := s.flightRecords[flightID]
	if !ok {
		return domain.FlightRecord{}, durable.ErrNotFound
	}
	return flight, nil
}

// ListFlightRecords returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListFlightRecords.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the []domain.FlightRecord value produced by ListFlightRecords.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordConformanceEvent durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordConformanceEvent.
//   - event: is the domain.ConformanceEvent value supplied to RecordConformanceEvent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListConformanceEvents returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListConformanceEvents.
//   - flightID: identifies the target flight.
//
// Returns:
//   - result: is the []domain.ConformanceEvent value produced by ListConformanceEvents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// UpsertConformanceSummary creates or replaces the supplied Store record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to UpsertConformanceSummary.
//   - summary: is the domain.ConformanceSummary value supplied to UpsertConformanceSummary.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpsertConformanceSummary(_ context.Context, summary domain.ConformanceSummary) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conformanceSummaries[conformanceSummaryKey(summary)] = summary
	return nil
}

// GetConformanceSummary returns the current conformance projection for an intent.
//
// Parameters:
//   - value: is the context.Context value supplied to GetConformanceSummary.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the *domain.ConformanceSummary value produced by GetConformanceSummary.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListConformanceSummaries returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListConformanceSummaries.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.ConformanceSummary value produced by ListConformanceSummaries.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordEvidence durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordEvidence.
//   - record: is the domain.EvidenceRecord value supplied to RecordEvidence.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordEvidence(_ context.Context, record domain.EvidenceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidenceRecords[record.ID] = record
	return nil
}

// ListEvidence returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListEvidence.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.EvidenceRecord value produced by ListEvidence.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordReportabilityReview durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordReportabilityReview.
//   - review: is the domain.ReportabilityReview value supplied to RecordReportabilityReview.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordReportabilityReview(_ context.Context, review domain.ReportabilityReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reportabilityReviews = append(s.reportabilityReviews, review)
	return nil
}

// ListReportabilityReviews returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListReportabilityReviews.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.ReportabilityReview value produced by ListReportabilityReviews.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordComplianceFinding durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordComplianceFinding.
//   - finding: is the domain.ComplianceFinding value supplied to RecordComplianceFinding.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListComplianceFindings returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListComplianceFindings.
//   - subjectType: is the string value supplied to ListComplianceFindings.
//   - subjectID: identifies the target subject.
//
// Returns:
//   - result: is the []domain.ComplianceFinding value produced by ListComplianceFindings.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListComplianceFindingsForIntent returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListComplianceFindingsForIntent.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.ComplianceFinding value produced by ListComplianceFindingsForIntent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordConflictFinding durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordConflictFinding.
//   - finding: is the domain.ConflictFinding value supplied to RecordConflictFinding.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListConflictFindings returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListConflictFindings.
//   - intentID: identifies the target intent.
//   - intentVersion: is the int value supplied to ListConflictFindings.
//
// Returns:
//   - result: is the []domain.ConflictFinding value produced by ListConflictFindings.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ReplaceConflictFindings atomically replaces the selected Store records.
//
// Parameters:
//   - value: is the context.Context value supplied to ReplaceConflictFindings.
//   - intentID: identifies the target intent.
//   - intentVersion: is the int value supplied to ReplaceConflictFindings.
//   - ruleVersion: is the string value supplied to ReplaceConflictFindings.
//   - findings: is the []domain.ConflictFinding value supplied to ReplaceConflictFindings.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// UpsertOperationsPersonnel creates or replaces the supplied Store record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to UpsertOperationsPersonnel.
//   - person: is the domain.OperationsPersonnel value supplied to UpsertOperationsPersonnel.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) UpsertOperationsPersonnel(_ context.Context, person domain.OperationsPersonnel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.personnel[person.ID] = person
	return nil
}

// GetOperationsPersonnel returns one operations-personnel record by identity.
//
// Parameters:
//   - value: is the context.Context value supplied to GetOperationsPersonnel.
//   - personID: identifies the target person.
//
// Returns:
//   - result: is the domain.OperationsPersonnel value produced by GetOperationsPersonnel.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperationsPersonnel(_ context.Context, personID string) (domain.OperationsPersonnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	person, ok := s.personnel[personID]
	if !ok {
		return domain.OperationsPersonnel{}, durable.ErrNotFound
	}
	return person, nil
}

// RecordPersonnelAssignment durably records the supplied Store data.
//
// Parameters:
//   - value: is the context.Context value supplied to RecordPersonnelAssignment.
//   - assignment: is the domain.PersonnelAssignment value supplied to RecordPersonnelAssignment.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordPersonnelAssignment(_ context.Context, assignment domain.PersonnelAssignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.personnelAssignments = append(s.personnelAssignments, assignment)
	return nil
}

// ListPersonnelAssignments returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListPersonnelAssignments.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.PersonnelAssignment value produced by ListPersonnelAssignments.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
