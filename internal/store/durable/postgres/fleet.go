package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CreateAircraft persists an aircraft used by mission and flight bindings.
func (s *Store) CreateAircraft(ctx context.Context, aircraft domain.Aircraft) error {
	raw, err := json.Marshal(aircraft)
	if err != nil {
		return fmt.Errorf("encode aircraft: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO aircraft (id,operator_id,agent_id,created_at,updated_at,data) VALUES ($1,$2,$3,$4,$5,$6)`,
		aircraft.ID, aircraft.OperatorID, aircraft.AgentID, aircraft.CreatedAt, aircraft.UpdatedAt, raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return durable.ErrAlreadyExists
		}
		return fmt.Errorf("insert aircraft: %w", err)
	}
	return nil
}

// GetAircraft returns one durable aircraft by identity.
func (s *Store) GetAircraft(ctx context.Context, aircraftID string) (domain.Aircraft, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT data FROM aircraft WHERE id=$1`, aircraftID).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return domain.Aircraft{}, durable.ErrNotFound
	} else if err != nil {
		return domain.Aircraft{}, fmt.Errorf("get aircraft: %w", err)
	}
	var aircraft domain.Aircraft
	if err := json.Unmarshal(raw, &aircraft); err != nil {
		return domain.Aircraft{}, fmt.Errorf("decode aircraft: %w", err)
	}
	return aircraft, nil
}

// ListAircraft returns all durable aircraft in stable identity order.
func (s *Store) ListAircraft(ctx context.Context) ([]domain.Aircraft, error) {
	rows, err := s.pool.Query(ctx, `SELECT data FROM aircraft ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list aircraft: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Aircraft, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan aircraft: %w", err)
		}
		var aircraft domain.Aircraft
		if err := json.Unmarshal(raw, &aircraft); err != nil {
			return nil, fmt.Errorf("decode aircraft: %w", err)
		}
		result = append(result, aircraft)
	}
	return result, rows.Err()
}

// CreateFlightRecord persists one flight lifecycle record.
func (s *Store) CreateFlightRecord(ctx context.Context, flight domain.FlightRecord) error {
	raw, err := json.Marshal(flight)
	if err != nil {
		return fmt.Errorf("encode flight record: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO flight_records (id,operator_id,aircraft_id,intent_id,intent_version,status,started_at,data) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		flight.ID, flight.OperatorID, flight.AircraftID, flight.IntentID, flight.IntentVersion, flight.Status, flight.StartedAt, raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return durable.ErrAlreadyExists
		}
		return fmt.Errorf("insert flight record: %w", err)
	}
	return nil
}

// UpdateFlightRecord replaces a flight under an optimistic status fence.
func (s *Store) UpdateFlightRecord(ctx context.Context, flight domain.FlightRecord, expectedStatus domain.FlightStatus) error {
	raw, err := json.Marshal(flight)
	if err != nil {
		return fmt.Errorf("encode flight record: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE flight_records SET operator_id=$1,aircraft_id=$2,intent_id=$3,intent_version=$4,status=$5,started_at=$6,data=$7 WHERE id=$8 AND status=$9`,
		flight.OperatorID, flight.AircraftID, flight.IntentID, flight.IntentVersion, flight.Status, flight.StartedAt, raw, flight.ID, expectedStatus)
	if err != nil {
		return fmt.Errorf("update flight record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, getErr := s.GetFlightRecord(ctx, flight.ID); errors.Is(getErr, durable.ErrNotFound) {
			return durable.ErrNotFound
		}
		return durable.ErrVersionConflict
	}
	return nil
}

// GetFlightRecord returns one durable flight by identity.
func (s *Store) GetFlightRecord(ctx context.Context, flightID string) (domain.FlightRecord, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT data FROM flight_records WHERE id=$1`, flightID).Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return domain.FlightRecord{}, durable.ErrNotFound
	} else if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get flight record: %w", err)
	}
	var flight domain.FlightRecord
	if err := json.Unmarshal(raw, &flight); err != nil {
		return domain.FlightRecord{}, fmt.Errorf("decode flight record: %w", err)
	}
	return flight, nil
}

// ListFlightRecords returns durable flights for an optional aircraft scope.
func (s *Store) ListFlightRecords(ctx context.Context, aircraftID string) ([]domain.FlightRecord, error) {
	query := `SELECT data FROM flight_records ORDER BY started_at DESC,id`
	args := []any{}
	if aircraftID != "" {
		query = `SELECT data FROM flight_records WHERE aircraft_id=$1 ORDER BY started_at DESC,id`
		args = append(args, aircraftID)
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list flight records: %w", err)
	}
	defer rows.Close()
	result := make([]domain.FlightRecord, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan flight record: %w", err)
		}
		var flight domain.FlightRecord
		if err := json.Unmarshal(raw, &flight); err != nil {
			return nil, fmt.Errorf("decode flight record: %w", err)
		}
		result = append(result, flight)
	}
	return result, rows.Err()
}

// StartFlightWithCurrentMissionDeployment atomically checks the current active
// intent and exact verified mission deployment under the flight lifecycle lock.
func (s *Store) StartFlightWithCurrentMissionDeployment(ctx context.Context, flight domain.FlightRecord, expectedStatus domain.FlightStatus) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin flight start: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentStatus domain.FlightStatus
	var intentID, aircraftID string
	var intentVersion int
	if err := tx.QueryRow(ctx, `SELECT status,intent_id,intent_version,aircraft_id FROM flight_records WHERE id=$1 FOR UPDATE`, flight.ID).
		Scan(&currentStatus, &intentID, &intentVersion, &aircraftID); errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock flight record: %w", err)
	}
	if currentStatus != expectedStatus {
		return durable.ErrVersionConflict
	}
	if err := lockIntent(ctx, tx, intentID); err != nil {
		return err
	}
	var currentIntentVersion int
	var currentIntentAircraft, currentIntentStatus string
	if err := tx.QueryRow(ctx, `SELECT version,aircraft_id,data->>'status' FROM operational_intents WHERE id=$1 ORDER BY version DESC LIMIT 1 FOR UPDATE`, intentID).
		Scan(&currentIntentVersion, &currentIntentAircraft, &currentIntentStatus); errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock current operational intent: %w", err)
	}
	if currentIntentVersion != intentVersion || currentIntentAircraft != aircraftID || domain.IntentStatus(currentIntentStatus) != domain.IntentStatusActive {
		return durable.ErrVersionConflict
	}
	var missionID, missionDigest string
	if err := tx.QueryRow(ctx, `SELECT id,mission_digest FROM missions WHERE flight_id=$1 ORDER BY version DESC LIMIT 1`, flight.ID).
		Scan(&missionID, &missionDigest); errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrVersionConflict
	} else if err != nil {
		return fmt.Errorf("get current mission for flight start: %w", err)
	}
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mission_deployments WHERE flight_id=$1 AND mission_id=$2 AND status IN ('pending','outcome_unknown'))`, flight.ID, missionID).Scan(&pending); err != nil {
		return fmt.Errorf("check uncertain mission deployment: %w", err)
	}
	if pending {
		return durable.ErrVersionConflict
	}
	var verified bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mission_deployments WHERE flight_id=$1 AND mission_id=$2 AND data->'deployment'->>'mission_digest'=$3 AND status IN ('applied','already_applied'))`, flight.ID, missionID, missionDigest).Scan(&verified); err != nil {
		return fmt.Errorf("check verified mission deployment: %w", err)
	}
	if !verified {
		return durable.ErrVersionConflict
	}
	raw, err := json.Marshal(flight)
	if err != nil {
		return fmt.Errorf("encode active flight: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE flight_records SET status=$1,started_at=$2,data=$3 WHERE id=$4 AND status=$5`, flight.Status, flight.StartedAt, raw, flight.ID, expectedStatus)
	if err != nil {
		return fmt.Errorf("activate flight: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return durable.ErrVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit flight start: %w", err)
	}
	return nil
}
