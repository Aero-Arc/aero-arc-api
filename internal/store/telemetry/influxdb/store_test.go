package influxdb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	query  string
	params map[string]any
	rows   []map[string]any
	err    error
	closed bool
}

func (f *fakeRunner) Query(_ context.Context, query string, params map[string]any) ([]map[string]any, error) {
	f.query, f.params = query, params
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
