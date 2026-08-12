package influxdb

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
	influxdb3 "github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
)

const (
	tableName                  = "aircraft_telemetry"
	messageName                = "global_position_int"
	messageBatteryStatus       = "battery_status"
	messageHeartbeat           = "heartbeat"
	messageSystemStatus        = "sys_status"
	messageVFRHUD              = "vfr_hud"
	messageExtendedSystemState = "extended_sys_state"
	messageGPSRaw              = "gps_raw_int"
)

type queryRunner interface {
	Query(context.Context, string, map[string]any) ([]map[string]any, error)
	Close() error
}

type Store struct{ runner queryRunner }

type sampleWindow uint8

const (
	earliestWindow sampleWindow = iota + 1
	latestWindow
)

type sampleWindowPolicy struct {
	sqlOrder      string
	reverseResult bool
}

func (w sampleWindow) policy() (sampleWindowPolicy, error) {
	switch w {
	case earliestWindow:
		return sampleWindowPolicy{sqlOrder: "ASC"}, nil
	case latestWindow:
		return sampleWindowPolicy{sqlOrder: "DESC", reverseResult: true}, nil
	default:
		return sampleWindowPolicy{}, fmt.Errorf("invalid sample window: %d", w)
	}
}

func New(host, token, database string) (*Store, error) {
	client, err := influxdb3.New(influxdb3.ClientConfig{Host: host, Token: token, Database: database})
	if err != nil {
		return nil, fmt.Errorf("create influxdb client: %w", err)
	}
	return &Store{runner: &clientRunner{client: client}}, nil
}

func newWithRunner(runner queryRunner) *Store { return &Store{runner: runner} }
func (s *Store) Close() error                 { return s.runner.Close() }

func (s *Store) GetLatestAircraftStates(ctx context.Context, aircraftIDs []string) (map[string]domain.AircraftTelemetryState, error) {
	states := make(map[string]domain.AircraftTelemetryState, len(aircraftIDs))
	unique := make([]string, 0, len(aircraftIDs))
	for _, aircraftID := range aircraftIDs {
		if strings.TrimSpace(aircraftID) == "" {
			continue
		}
		if _, exists := states[aircraftID]; exists {
			continue
		}
		states[aircraftID] = domain.AircraftTelemetryState{}
		unique = append(unique, aircraftID)
	}
	if len(unique) == 0 {
		return states, nil
	}

	decoders := map[string]struct {
		decode func(map[string]any) (any, error)
		assign func(*domain.AircraftTelemetryState, any)
	}{
		messageName: {decodePosition, func(state *domain.AircraftTelemetryState, value any) {
			state.Position = value.(*domain.PositionTelemetry)
		}},
		messageBatteryStatus: {decodeBattery, func(state *domain.AircraftTelemetryState, value any) {
			state.Battery = value.(*domain.BatteryTelemetry)
		}},
		messageHeartbeat: {decodeVehicle, func(state *domain.AircraftTelemetryState, value any) {
			state.Vehicle = value.(*domain.VehicleTelemetry)
		}},
		messageSystemStatus: {decodeSystem, func(state *domain.AircraftTelemetryState, value any) { state.System = value.(*domain.SystemTelemetry) }},
		messageVFRHUD:       {decodeHUD, func(state *domain.AircraftTelemetryState, value any) { state.HUD = value.(*domain.HUDTelemetry) }},
		messageExtendedSystemState: {decodeExtendedState, func(state *domain.AircraftTelemetryState, value any) {
			state.ExtendedState = value.(*domain.ExtendedStateTelemetry)
		}},
		messageGPSRaw: {decodeGPS, func(state *domain.AircraftTelemetryState, value any) { state.GPS = value.(*domain.GPSTelemetry) }},
	}
	rows, err := s.queryLatestRowsByAircraft(ctx, unique)
	if err != nil {
		if isMissingColumn(err, "aircraft_id") {
			return states, nil
		}
		return nil, err
	}
	for _, row := range rows {
		aircraftID := stringValue(row["aircraft_id"])
		state, requested := states[aircraftID]
		if !requested {
			continue
		}
		item, supported := decoders[stringValue(row["message_name"])]
		if !supported {
			continue
		}
		value, err := item.decode(row)
		if err != nil {
			return nil, err
		}
		item.assign(&state, value)
		states[aircraftID] = state
	}
	return states, nil
}

func (s *Store) queryLatestRowsByAircraft(ctx context.Context, aircraftIDs []string) ([]map[string]any, error) {
	params := make(map[string]any, len(aircraftIDs)+7)
	aircraftBindings := make([]string, 0, len(aircraftIDs))
	for index, aircraftID := range aircraftIDs {
		name := fmt.Sprintf("aircraft_id_%d", index)
		aircraftBindings = append(aircraftBindings, "$"+name)
		params[name] = aircraftID
	}
	messages := []string{messageName, messageBatteryStatus, messageHeartbeat, messageSystemStatus, messageVFRHUD, messageExtendedSystemState, messageGPSRaw}
	messageBindings := make([]string, 0, len(messages))
	for index, message := range messages {
		name := fmt.Sprintf("message_name_%d", index)
		messageBindings = append(messageBindings, "$"+name)
		params[name] = message
	}
	query := fmt.Sprintf(`SELECT * FROM (
SELECT *, ROW_NUMBER() OVER (PARTITION BY aircraft_id, message_name ORDER BY time DESC) AS latest_rank
FROM %q
WHERE message_name IN (%s) AND aircraft_id IN (%s)
) AS latest WHERE latest_rank = 1`, tableName, strings.Join(messageBindings, ", "), strings.Join(aircraftBindings, ", "))
	rows, err := s.runner.Query(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("query latest aircraft telemetry: %w", err)
	}
	return rows, nil
}

func (s *Store) GetLatestSample(ctx context.Context, aircraftID string) (*domain.TelemetrySample, error) {
	samples, err := s.latestSamplesChronological(ctx, `aircraft_id = $aircraft_id`, map[string]any{"aircraft_id": aircraftID}, 1)
	if err != nil || len(samples) == 0 {
		return nil, err
	}
	return &samples[0], nil
}

func (s *Store) QueryAircraftSamples(ctx context.Context, aircraftID string, limit int) ([]domain.TelemetrySample, error) {
	return s.latestSamplesChronological(ctx, `aircraft_id = $aircraft_id`, map[string]any{"aircraft_id": aircraftID}, limit)
}

func (s *Store) QueryFlightSamples(ctx context.Context, flightID string, limit int) ([]domain.TelemetrySample, error) {
	samples, err := s.earliestSamplesChronological(ctx, `flight_id = $flight_id`, map[string]any{"flight_id": flightID}, limit)
	if err != nil && isMissingColumn(err, "flight_id") {
		return []domain.TelemetrySample{}, nil
	}
	return samples, err
}

func (s *Store) latestSamplesChronological(ctx context.Context, predicate string, params map[string]any, limit int) ([]domain.TelemetrySample, error) {
	return s.samplesChronological(ctx, predicate, params, limit, latestWindow)
}

func (s *Store) earliestSamplesChronological(ctx context.Context, predicate string, params map[string]any, limit int) ([]domain.TelemetrySample, error) {
	return s.samplesChronological(ctx, predicate, params, limit, earliestWindow)
}

// samplesChronological selects the requested limited window and always returns
// its samples from oldest to newest.
func (s *Store) samplesChronological(ctx context.Context, predicate string, params map[string]any, limit int, window sampleWindow) ([]domain.TelemetrySample, error) {
	policy, err := window.policy()
	if err != nil {
		return nil, err
	}
	rows, err := s.querySampleRows(ctx, predicate, params, limit, policy.sqlOrder)
	if err != nil {
		return nil, err
	}
	samples := make([]domain.TelemetrySample, 0, len(rows))
	for _, row := range rows {
		sample, err := sampleFromRow(row)
		if err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	if policy.reverseResult {
		for left, right := 0, len(samples)-1; left < right; left, right = left+1, right-1 {
			samples[left], samples[right] = samples[right], samples[left]
		}
	}
	return samples, nil
}

func (s *Store) querySampleRows(ctx context.Context, predicate string, params map[string]any, limit int, sqlOrder string) ([]map[string]any, error) {
	if limit <= 0 {
		limit = telemetry.DefaultSampleLimit
	}
	// SELECT * is intentional: optional tags and fields do not become InfluxDB
	// table columns until a point first supplies them. Projecting a sparse column
	// explicitly would make otherwise valid position reads fail on a new table.
	query := fmt.Sprintf(`SELECT * FROM %q WHERE message_name = $message_name AND %s ORDER BY time %s LIMIT %d`, tableName, predicate, sqlOrder, limit)
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

func observationFromRow(row map[string]any) (domain.TelemetryObservation, error) {
	recordedAt, ok := row["time"].(time.Time)
	if !ok {
		return domain.TelemetryObservation{}, fmt.Errorf("decode influxdb telemetry: time has type %T", row["time"])
	}
	return domain.TelemetryObservation{
		RecordedAt: recordedAt,
		FrameID:    stringValue(row["frame_id"]), RelayID: stringValue(row["relay_id"]),
		SessionID: stringValue(row["session_id"]), TimestampSource: stringValue(row["timestamp_source"]),
		OperatorID: stringValue(row["operator_id"]), IntentID: stringValue(row["intent_id"]),
		IntentVersion: intValue(row["intent_version"]), FlightID: stringValue(row["flight_id"]),
	}, nil
}

func decodePosition(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	latitude, err := requiredFloat(row, "latitude_deg")
	if err != nil {
		return nil, err
	}
	longitude, err := requiredFloat(row, "longitude_deg")
	if err != nil {
		return nil, err
	}
	return &domain.PositionTelemetry{TelemetryObservation: observation, LatitudeDeg: latitude, LongitudeDeg: longitude,
		AltitudeMSLM: optionalFloat(row, "altitude_msl_m"), RelativeAltitudeM: optionalFloat(row, "relative_altitude_m"),
		VelocityNorthMPS: optionalFloat(row, "velocity_north_mps"), VelocityEastMPS: optionalFloat(row, "velocity_east_mps"),
		VelocityDownMPS: optionalFloat(row, "velocity_down_mps"), GroundspeedMPS: optionalFloat(row, "groundspeed_mps"), HeadingDeg: optionalFloat(row, "heading_deg")}, nil
}

func decodeBattery(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	batteryID, err := requiredUint(row, "battery_id")
	if err != nil {
		return nil, err
	}
	return &domain.BatteryTelemetry{TelemetryObservation: observation, BatteryID: batteryID,
		BatteryFunction: stringValue(row["battery_function"]), BatteryType: stringValue(row["battery_type"]),
		BatteryChargeState: stringValue(row["battery_charge_state"]), BatteryMode: stringValue(row["battery_mode"]),
		BatteryTemperatureC: optionalFloat(row, "battery_temperature_c"), BatteryVoltageV: optionalFloat(row, "battery_voltage_v"),
		BatteryCurrentA: optionalFloat(row, "battery_current_a"), BatteryConsumedMAH: optionalInt64(row, "battery_consumed_mah"),
		BatteryConsumedWH: optionalFloat(row, "battery_consumed_wh"), BatteryRemainingPct: optionalFloat(row, "battery_remaining_pct"),
		BatteryTimeRemainingS: optionalInt64(row, "battery_time_remaining_s")}, nil
}

func decodeVehicle(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	return &domain.VehicleTelemetry{TelemetryObservation: observation, VehicleType: stringValue(row["vehicle_type"]),
		AutopilotType: stringValue(row["autopilot_type"]), BaseMode: stringValue(row["base_mode"]),
		CustomMode: optionalUint(row, "custom_mode"), SystemStatus: stringValue(row["system_status"]), MAVLinkVersion: optionalUint(row, "mavlink_version")}, nil
}

func decodeSystem(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	return &domain.SystemTelemetry{TelemetryObservation: observation,
		MainloopLoadPct: optionalFloat(row, "mainloop_load_pct"), CommunicationDropRatePct: optionalFloat(row, "communication_drop_rate_pct"),
		CommunicationErrorCount: optionalUint(row, "communication_error_count"), AutopilotErrorCount1: optionalUint(row, "autopilot_error_count_1"),
		AutopilotErrorCount2: optionalUint(row, "autopilot_error_count_2"), AutopilotErrorCount3: optionalUint(row, "autopilot_error_count_3"),
		AutopilotErrorCount4: optionalUint(row, "autopilot_error_count_4"), SensorsPresent: stringValue(row["sensors_present"]),
		SensorsEnabled: stringValue(row["sensors_enabled"]), SensorsHealth: stringValue(row["sensors_health"]),
		SensorsPresentExtended: stringValue(row["sensors_present_extended"]), SensorsEnabledExtended: stringValue(row["sensors_enabled_extended"]),
		SensorsHealthExtended: stringValue(row["sensors_health_extended"])}, nil
}

func decodeHUD(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	return &domain.HUDTelemetry{TelemetryObservation: observation, AirspeedMPS: optionalFloat(row, "airspeed_mps"),
		GroundspeedMPS: optionalFloat(row, "groundspeed_mps"), HeadingDeg: optionalFloat(row, "heading_deg"),
		ThrottlePct: optionalFloat(row, "throttle_pct"), AltitudeMSLM: optionalFloat(row, "altitude_msl_m"), ClimbRateMPS: optionalFloat(row, "climb_rate_mps")}, nil
}

func decodeExtendedState(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	return &domain.ExtendedStateTelemetry{TelemetryObservation: observation, VTOLState: stringValue(row["vtol_state"]), LandedState: stringValue(row["landed_state"])}, nil
}

func decodeGPS(row map[string]any) (any, error) {
	observation, err := observationFromRow(row)
	if err != nil {
		return nil, err
	}
	return &domain.GPSTelemetry{TelemetryObservation: observation, FixType: stringValue(row["gps_fix_type"]),
		LatitudeDeg: optionalFloat(row, "gps_latitude_deg"), LongitudeDeg: optionalFloat(row, "gps_longitude_deg"),
		AltitudeMSLM: optionalFloat(row, "gps_altitude_msl_m"), AltitudeEllipsoidM: optionalFloat(row, "gps_altitude_ellipsoid_m"),
		HDOP: optionalFloat(row, "gps_hdop"), VDOP: optionalFloat(row, "gps_vdop"), GroundspeedMPS: optionalFloat(row, "gps_groundspeed_mps"),
		CourseOverGroundDeg: optionalFloat(row, "gps_course_over_ground_deg"), SatellitesVisible: optionalUint(row, "gps_satellites_visible"),
		HorizontalAccuracyM: optionalFloat(row, "gps_horizontal_accuracy_m"), VerticalAccuracyM: optionalFloat(row, "gps_vertical_accuracy_m"),
		SpeedAccuracyMPS: optionalFloat(row, "gps_speed_accuracy_mps"), HeadingAccuracyDeg: optionalFloat(row, "gps_heading_accuracy_deg"), YawDeg: optionalFloat(row, "gps_yaw_deg")}, nil
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

func optionalFloat(row map[string]any, key string) *float64 {
	v, ok := row[key]
	if !ok || v == nil {
		return nil
	}
	value, ok := numericFloat(v)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func requiredUint(row map[string]any, key string) (uint64, error) {
	value := optionalUint(row, key)
	if value == nil {
		return 0, fmt.Errorf("decode influxdb telemetry: missing or invalid %s", key)
	}
	return *value, nil
}

func optionalUint(row map[string]any, key string) *uint64 {
	v, ok := row[key]
	if !ok || v == nil {
		return nil
	}
	var value uint64
	switch number := v.(type) {
	case uint64:
		value = number
	case uint32:
		value = uint64(number)
	case int64:
		if number < 0 {
			return nil
		}
		value = uint64(number)
	case int32:
		if number < 0 {
			return nil
		}
		value = uint64(number)
	case int:
		if number < 0 {
			return nil
		}
		value = uint64(number)
	default:
		return nil
	}
	return &value
}

func optionalInt64(row map[string]any, key string) *int64 {
	v, ok := row[key]
	if !ok || v == nil {
		return nil
	}
	var value int64
	switch number := v.(type) {
	case int64:
		value = number
	case int32:
		value = int64(number)
	case int:
		value = int64(number)
	case uint64:
		if number > math.MaxInt64 {
			return nil
		}
		value = int64(number)
	case uint32:
		value = int64(number)
	default:
		return nil
	}
	return &value
}

func numericFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	default:
		return 0, false
	}
}

func floatValue(v any) float64 {
	value, _ := numericFloat(v)
	return value
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
