package influxdb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	query         string
	queries       []string
	params        map[string]any
	rows          []map[string]any
	rowsByMessage map[string][]map[string]any
	err           error
	closed        bool
}

func (f *fakeRunner) Query(_ context.Context, query string, params map[string]any) ([]map[string]any, error) {
	f.query, f.params = query, params
	f.queries = append(f.queries, query)
	if f.rowsByMessage != nil {
		if message := stringValue(params["message_name"]); message != "" {
			return f.rowsByMessage[message], f.err
		}
		rows := make([]map[string]any, 0)
		for message, messageRows := range f.rowsByMessage {
			for _, row := range messageRows {
				cloned := make(map[string]any, len(row)+1)
				for key, value := range row {
					cloned[key] = value
				}
				cloned["message_name"] = message
				rows = append(rows, cloned)
			}
		}
		return rows, f.err
	}
	return f.rows, f.err
}
func (f *fakeRunner) Close() error { f.closed = true; return nil }

func TestGetLatestSample(t *testing.T) {
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	runner := &fakeRunner{rows: []map[string]any{{
		"time": now, "frame_id": "agent:7", "operator_id": "operator-1", "aircraft_id": "aircraft-1",
		"flight_id": "flight-1", "intent_id": "intent-1", "intent_version": int64(3),
		"latitude_deg": 41.8781, "longitude_deg": -87.6291, "altitude_msl_m": 123.45,
		"groundspeed_mps": 3.2, "heading_deg": 92.5,
	}}}
	store := newWithRunner(runner)
	sample, err := store.GetLatestSample(context.Background(), "aircraft-1")
	if err != nil {
		t.Fatal(err)
	}
	if sample == nil || sample.ID != "agent:7" || sample.AircraftID != "aircraft-1" || sample.FlightID != "flight-1" {
		t.Fatalf("unexpected sample: %#v", sample)
	}
	if sample.Latitude != 41.8781 || sample.AltitudeM != 123.45 || sample.VelocityMPS != 3.2 {
		t.Fatalf("unexpected values: %#v", sample)
	}
	if !strings.Contains(runner.query, `FROM "aircraft_telemetry"`) || !strings.Contains(runner.query, "ORDER BY time DESC LIMIT 1") {
		t.Fatalf("unexpected query: %s", runner.query)
	}
	if runner.params["message_name"] != "global_position_int" || runner.params["aircraft_id"] != "aircraft-1" {
		t.Fatalf("unexpected params: %#v", runner.params)
	}
}

func TestGetLatestAircraftStatesQueriesAndDecodesIndependentGroups(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	base := func(aircraftID string) map[string]any {
		return map[string]any{"time": now, "aircraft_id": aircraftID, "frame_id": "frame-1", "relay_id": "relay-1", "session_id": "session-1", "timestamp_source": "agent_capture"}
	}
	position := base("aircraft-1")
	position["latitude_deg"], position["longitude_deg"] = 41.1, -87.2
	position["relative_altitude_m"], position["groundspeed_mps"] = 22.5, 8.75
	battery := base("aircraft-1")
	battery["battery_id"], battery["battery_remaining_pct"], battery["battery_voltage_v"] = uint64(2), 74.0, 23.4
	vehicle := base("aircraft-1")
	vehicle["vehicle_type"], vehicle["system_status"], vehicle["custom_mode"] = "quadrotor", "active", uint64(4)
	gps := base("aircraft-2")
	gps["gps_fix_type"], gps["gps_satellites_visible"], gps["gps_hdop"] = "3d_fix", uint64(14), 0.8
	runner := &fakeRunner{rowsByMessage: map[string][]map[string]any{
		messageName: {position}, messageBatteryStatus: {battery}, messageHeartbeat: {vehicle}, messageGPSRaw: {gps},
	}}

	states, err := newWithRunner(runner).GetLatestAircraftStates(context.Background(), []string{"aircraft-1", "aircraft-2", "aircraft-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.queries) != 1 {
		t.Fatalf("queries = %d, want one batched latest-state query", len(runner.queries))
	}
	for _, query := range runner.queries {
		if !strings.Contains(query, "ROW_NUMBER() OVER (PARTITION BY aircraft_id, message_name ORDER BY time DESC)") {
			t.Fatalf("query is not bounded to latest per aircraft: %s", query)
		}
		if strings.Count(query, "$aircraft_id_") != 2 {
			t.Fatalf("query has duplicate/missing bindings: %s", query)
		}
		if strings.Count(query, "$message_name_") != 7 {
			t.Fatalf("query does not bind all message groups: %s", query)
		}
	}
	first := states["aircraft-1"]
	if first.Position == nil || first.Position.RelativeAltitudeM == nil || *first.Position.RelativeAltitudeM != 22.5 {
		t.Fatalf("position = %#v", first.Position)
	}
	if first.Battery == nil || first.Battery.BatteryID != 2 || first.Battery.BatteryRemainingPct == nil || *first.Battery.BatteryRemainingPct != 74 {
		t.Fatalf("battery = %#v", first.Battery)
	}
	if first.Vehicle == nil || first.Vehicle.SystemStatus != "active" || first.Vehicle.CustomMode == nil || *first.Vehicle.CustomMode != 4 {
		t.Fatalf("vehicle = %#v", first.Vehicle)
	}
	second := states["aircraft-2"]
	if second.GPS == nil || second.GPS.SatellitesVisible == nil || *second.GPS.SatellitesVisible != 14 {
		t.Fatalf("gps = %#v", second.GPS)
	}
}

func TestGetLatestAircraftStatesIsolatesMalformedObservation(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	malformedBattery := map[string]any{
		"time": now, "aircraft_id": "aircraft-1", "battery_remaining_pct": 50.0,
	}
	validPosition := map[string]any{
		"time": now, "aircraft_id": "aircraft-1", "latitude_deg": 41.1, "longitude_deg": -87.2,
	}
	validGPS := map[string]any{
		"time": now, "aircraft_id": "aircraft-2", "gps_fix_type": "3d_fix", "gps_satellites_visible": uint64(12),
	}
	runner := &fakeRunner{rowsByMessage: map[string][]map[string]any{
		messageBatteryStatus: {malformedBattery},
		messageName:          {validPosition},
		messageGPSRaw:        {validGPS},
	}}

	states, err := newWithRunner(runner).GetLatestAircraftStates(context.Background(), []string{"aircraft-1", "aircraft-2"})
	if err != nil {
		t.Fatal(err)
	}
	first := states["aircraft-1"]
	if first.Battery != nil {
		t.Fatalf("malformed battery = %#v, want omitted observation", first.Battery)
	}
	if first.Position == nil || first.Position.LatitudeDeg != 41.1 {
		t.Fatalf("valid position lost: %#v", first.Position)
	}
	second := states["aircraft-2"]
	if second.GPS == nil || second.GPS.SatellitesVisible == nil || *second.GPS.SatellitesVisible != 12 {
		t.Fatalf("valid second-aircraft GPS lost: %#v", second.GPS)
	}
}

func TestGetLatestAircraftStatesReturnsQueryFailure(t *testing.T) {
	runner := &fakeRunner{
		rowsByMessage: map[string][]map[string]any{messageBatteryStatus: {{
			"time": time.Now().UTC(), "aircraft_id": "aircraft-1", "battery_remaining_pct": 50.0,
		}}},
		err: errors.New("transport unavailable"),
	}
	states, err := newWithRunner(runner).GetLatestAircraftStates(context.Background(), []string{"aircraft-1"})
	if err == nil || !strings.Contains(err.Error(), "query latest aircraft telemetry") || !strings.Contains(err.Error(), "transport unavailable") {
		t.Fatalf("error = %v, want wrapped query failure", err)
	}
	if states != nil {
		t.Fatalf("states = %#v, want nil on query failure", states)
	}
}

func TestQueryFlightSamplesReturnsChronologicalOrder(t *testing.T) {
	older := time.Date(2026, 7, 12, 19, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	row := func(at time.Time, id string, latitude float64) map[string]any {
		return map[string]any{"time": at, "frame_id": id, "aircraft_id": "aircraft-1", "flight_id": "flight-1", "latitude_deg": latitude, "longitude_deg": -87.0}
	}
	runner := &fakeRunner{rows: []map[string]any{row(older, "old", 41), row(newer, "new", 42)}}
	samples, err := newWithRunner(runner).QueryFlightSamples(context.Background(), "flight-1", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].ID != "old" || samples[1].ID != "new" {
		t.Fatalf("unexpected order: %#v", samples)
	}
	if !strings.Contains(runner.query, "flight_id = $flight_id") || !strings.Contains(runner.query, "ORDER BY time ASC LIMIT 25") {
		t.Fatalf("unexpected query: %s", runner.query)
	}
}

func TestQueryAircraftSamplesReturnsLatestWindowChronologically(t *testing.T) {
	older := time.Date(2026, 7, 12, 19, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	row := func(at time.Time, id string, latitude float64) map[string]any {
		return map[string]any{"time": at, "frame_id": id, "aircraft_id": "aircraft-1", "latitude_deg": latitude, "longitude_deg": -87.0}
	}
	runner := &fakeRunner{rows: []map[string]any{row(newer, "new", 42), row(older, "old", 41)}}
	samples, err := newWithRunner(runner).QueryAircraftSamples(context.Background(), "aircraft-1", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].ID != "old" || samples[1].ID != "new" {
		t.Fatalf("unexpected order: %#v", samples)
	}
	if !strings.Contains(runner.query, "aircraft_id = $aircraft_id") || !strings.Contains(runner.query, "ORDER BY time DESC LIMIT 25") {
		t.Fatalf("unexpected query: %s", runner.query)
	}
}

func TestSamplesChronologicalRejectsInvalidWindow(t *testing.T) {
	runner := &fakeRunner{}
	_, err := newWithRunner(runner).samplesChronological(context.Background(), "1 = 1", map[string]any{}, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "invalid sample window") {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.query != "" {
		t.Fatalf("query executed for invalid window: %s", runner.query)
	}
}

func TestZeroLimitUsesDefaultSampleLimit(t *testing.T) {
	runner := &fakeRunner{}
	_, err := newWithRunner(runner).QueryAircraftSamples(context.Background(), "aircraft-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.query, "LIMIT 1000") {
		t.Fatalf("unexpected query: %s", runner.query)
	}
}

func TestGetLatestSampleNotFound(t *testing.T) {
	sample, err := newWithRunner(&fakeRunner{}).GetLatestSample(context.Background(), "missing")
	if err != nil || sample != nil {
		t.Fatalf("sample=%#v err=%v", sample, err)
	}
}

func TestQueryErrorAndDecodeError(t *testing.T) {
	_, err := newWithRunner(&fakeRunner{err: errors.New("unavailable")}).QueryAircraftSamples(context.Background(), "aircraft-1", 1)
	if err == nil || !strings.Contains(err.Error(), "query influxdb telemetry") {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = newWithRunner(&fakeRunner{rows: []map[string]any{{"time": time.Now(), "longitude_deg": 1.0}}}).QueryAircraftSamples(context.Background(), "aircraft-1", 1)
	if err == nil || !strings.Contains(err.Error(), "missing latitude_deg") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueryFlightSamplesTreatsAbsentSparseFlightColumnAsEmpty(t *testing.T) {
	runner := &fakeRunner{err: errors.New("No field named flight_id")}
	samples, err := newWithRunner(runner).QueryFlightSamples(context.Background(), "flight-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 0 {
		t.Fatalf("unexpected samples: %#v", samples)
	}
}

func TestClose(t *testing.T) {
	runner := &fakeRunner{}
	if err := newWithRunner(runner).Close(); err != nil {
		t.Fatal(err)
	}
	if !runner.closed {
		t.Fatal("runner was not closed")
	}
}
