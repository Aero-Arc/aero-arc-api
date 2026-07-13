package influxdb

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	influxdb3 "github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
)

const (
	tableName   = "aircraft_telemetry"
	messageName = "global_position_int"
)

type queryRunner interface {
	Query(context.Context, string, map[string]any) ([]map[string]any, error)
	Close() error
}

type Store struct{ runner queryRunner }

func New(host, token, database string) (*Store, error) {
	client, err := influxdb3.New(influxdb3.ClientConfig{Host: host, Token: token, Database: database})
	if err != nil {
		return nil, fmt.Errorf("create influxdb client: %w", err)
	}
	return &Store{runner: &clientRunner{client: client}}, nil
}

func newWithRunner(runner queryRunner) *Store { return &Store{runner: runner} }
func (s *Store) Close() error                 { return s.runner.Close() }

func (s *Store) GetLatestSample(ctx context.Context, aircraftID string) (*domain.TelemetrySample, error) {
	rows, err := s.query(ctx, `aircraft_id = $aircraft_id`, map[string]any{"aircraft_id": aircraftID}, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	sample, err := sampleFromRow(rows[0])
	if err != nil {
		return nil, err
	}
	return &sample, nil
}

func (s *Store) QueryAircraftSamples(ctx context.Context, aircraftID string, limit int) ([]domain.TelemetrySample, error) {
	return s.samples(ctx, `aircraft_id = $aircraft_id`, map[string]any{"aircraft_id": aircraftID}, limit)
}

func (s *Store) QueryFlightSamples(ctx context.Context, flightID string, limit int) ([]domain.TelemetrySample, error) {
	samples, err := s.samples(ctx, `flight_id = $flight_id`, map[string]any{"flight_id": flightID}, limit)
	if err != nil && isMissingColumn(err, "flight_id") {
		return []domain.TelemetrySample{}, nil
	}
	return samples, err
}

func (s *Store) samples(ctx context.Context, predicate string, params map[string]any, limit int) ([]domain.TelemetrySample, error) {
	rows, err := s.query(ctx, predicate, params, limit)
	if err != nil {
		return nil, err
	}
	samples := make([]domain.TelemetrySample, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		sample, err := sampleFromRow(rows[i])
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

func (s *Store) query(ctx context.Context, predicate string, params map[string]any, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 1000
	}
	// SELECT * is intentional: optional tags and fields do not become InfluxDB
	// table columns until a point first supplies them. Projecting a sparse column
	// explicitly would make otherwise valid position reads fail on a new table.
	query := fmt.Sprintf(`SELECT * FROM %q WHERE message_name = $message_name AND %s ORDER BY time DESC LIMIT %d`, tableName, predicate, limit)
	params["message_name"] = messageName
	rows, err := s.runner.Query(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("query influxdb telemetry: %w", err)
	}
	return rows, nil
}

func isMissingColumn(err error, column string) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, strings.ToLower(column)) &&
		(strings.Contains(message, "not found") || strings.Contains(message, "no field") || strings.Contains(message, "unknown column"))
}

func sampleFromRow(row map[string]any) (domain.TelemetrySample, error) {
	recordedAt, ok := row["time"].(time.Time)
	if !ok {
		return domain.TelemetrySample{}, fmt.Errorf("decode influxdb telemetry: time has type %T", row["time"])
	}
	latitude, err := requiredFloat(row, "latitude_deg")
	if err != nil {
		return domain.TelemetrySample{}, err
	}
	longitude, err := requiredFloat(row, "longitude_deg")
	if err != nil {
		return domain.TelemetrySample{}, err
	}
	return domain.TelemetrySample{
		ID:            stringValue(row["frame_id"]),
		OperatorID:    stringValue(row["operator_id"]),
		AircraftID:    stringValue(row["aircraft_id"]),
		IntentID:      stringValue(row["intent_id"]),
		IntentVersion: intValue(row["intent_version"]),
		FlightID:      stringValue(row["flight_id"]),
		RecordedAt:    recordedAt,
		Latitude:      latitude,
		Longitude:     longitude,
		AltitudeM:     floatValue(row["altitude_msl_m"]),
		VelocityMPS:   floatValue(row["groundspeed_mps"]),
		HeadingDeg:    floatValue(row["heading_deg"]),
	}, nil
}

func requiredFloat(row map[string]any, key string) (float64, error) {
	v, ok := row[key]
	if !ok || v == nil {
		return 0, fmt.Errorf("decode influxdb telemetry: missing %s", key)
	}
	f := floatValue(v)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("decode influxdb telemetry: invalid %s", key)
	}
	return f, nil
}

func floatValue(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case uint64:
		return float64(n)
	case uint32:
		return float64(n)
	default:
		return 0
	}
}

func intValue(v any) int {
	if s, ok := v.(string); ok {
		n, _ := strconv.Atoi(s)
		return n
	}
	return int(floatValue(v))
}
func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type clientRunner struct{ client *influxdb3.Client }

func (r *clientRunner) Query(ctx context.Context, query string, params map[string]any) ([]map[string]any, error) {
	iterator, err := r.client.QueryWithParameters(ctx, query, params)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0)
	for iterator.Next() {
		rows = append(rows, iterator.Value())
	}
	if err := iterator.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *clientRunner) Close() error { return r.client.Close() }
