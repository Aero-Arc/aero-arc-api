//go:build integration

package influxdb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	influxdb3 "github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
)

func TestLiveAircraftStateAgainstInfluxDB(t *testing.T) {
	client, err := influxdb3.New(influxdb3.ClientConfig{
		Host: integrationInfluxDB.host, Token: integrationInfluxDB.token, Database: integrationInfluxDB.database,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	aircraftID := fmt.Sprintf("aircraft-api-integration-%d", time.Now().UnixNano())
	now := time.Now().UTC().Add(-time.Second)
	point := func(message string, fields map[string]interface{}, offset time.Duration) *influxdb3.Point {
		fields["aircraft_id"] = aircraftID
		fields["relay_id"] = "relay-integration"
		fields["session_id"] = "session-integration"
		fields["timestamp_source"] = "agent_capture"
		return influxdb3.NewPoint(tableName, map[string]string{
			"agent_id": "agent-integration", "frame_id": message + "-frame", "message_name": message, "schema_version": "1",
		}, fields, now.Add(offset))
	}
	points := []*influxdb3.Point{
		point(messageName, map[string]interface{}{"latitude_deg": 41.88, "longitude_deg": -87.63, "relative_altitude_m": 31.5}, 0),
		point(messageBatteryStatus, map[string]interface{}{"battery_id": uint64(1), "battery_remaining_pct": 81.0, "battery_voltage_v": 24.2}, time.Millisecond),
		point(messageHeartbeat, map[string]interface{}{"vehicle_type": "quadrotor", "system_status": "active"}, 2*time.Millisecond),
		point(messageSystemStatus, map[string]interface{}{"mainloop_load_pct": 17.5, "communication_error_count": uint64(2)}, 3*time.Millisecond),
		point(messageVFRHUD, map[string]interface{}{"airspeed_mps": 9.5, "climb_rate_mps": 1.2}, 4*time.Millisecond),
		point(messageExtendedSystemState, map[string]interface{}{"landed_state": "in_air"}, 5*time.Millisecond),
		point(messageGPSRaw, map[string]interface{}{"gps_fix_type": "3d_fix", "gps_satellites_visible": uint64(13)}, 6*time.Millisecond),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.WritePoints(ctx, points); err != nil {
		t.Fatalf("write telemetry: %v", err)
	}

	store := newWithRunner(&clientRunner{client: client})
	var stateFound bool
	for !stateFound {
		states, queryErr := store.GetLatestAircraftStates(ctx, []string{aircraftID})
		if queryErr == nil {
			state := states[aircraftID]
			stateFound = state.Position != nil && state.Battery != nil && state.Vehicle != nil && state.System != nil && state.HUD != nil && state.ExtendedState != nil && state.GPS != nil
			if stateFound {
				if state.Position.LatitudeDeg != 41.88 || state.Battery.BatteryRemainingPct == nil || *state.Battery.BatteryRemainingPct != 81 || state.GPS.SatellitesVisible == nil || *state.GPS.SatellitesVisible != 13 {
					t.Fatalf("unexpected state: %#v", state)
				}
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("telemetry did not become queryable: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestLatestAircraftStateLookbackAgainstInfluxDB(t *testing.T) {
	client, err := influxdb3.New(influxdb3.ClientConfig{
		Host: integrationInfluxDB.host, Token: integrationInfluxDB.token, Database: integrationInfluxDB.database,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	now := time.Now().UTC()
	lookback := 2 * time.Minute
	aircraftID := fmt.Sprintf("aircraft-api-lookback-%d", now.UnixNano())
	commonTags := func(frameID, message string) map[string]string {
		return map[string]string{
			"agent_id": "agent-lookback", "frame_id": frameID, "message_name": message, "schema_version": "1",
		}
	}
	commonFields := func(fields map[string]interface{}) map[string]interface{} {
		fields["aircraft_id"] = aircraftID
		fields["relay_id"] = "relay-lookback"
		fields["session_id"] = "session-lookback"
		fields["timestamp_source"] = "agent_capture"
		return fields
	}
	points := []*influxdb3.Point{
		influxdb3.NewPoint(tableName, commonTags("old-battery", messageBatteryStatus), commonFields(map[string]interface{}{
			"battery_id": uint64(1), "battery_remaining_pct": 12.0,
		}), now.Add(-2*lookback)),
		influxdb3.NewPoint(tableName, commonTags("recent-position", messageName), commonFields(map[string]interface{}{
			"latitude_deg": 41.9, "longitude_deg": -87.7,
		}), now.Add(-time.Second)),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.WritePoints(ctx, points); err != nil {
		t.Fatalf("write telemetry: %v", err)
	}

	store := newWithRunnerPolicy(&clientRunner{client: client}, lookback, func() time.Time { return now })
	for {
		states, queryErr := store.GetLatestAircraftStates(ctx, []string{aircraftID})
		if queryErr == nil && states[aircraftID].Position != nil {
			state := states[aircraftID]
			if state.Position.LatitudeDeg != 41.9 {
				t.Fatalf("position = %#v, want recent point", state.Position)
			}
			if state.Battery != nil {
				t.Fatalf("battery = %#v, want old point outside lookback excluded", state.Battery)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("recent telemetry did not become queryable: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func TestTelemetrySampleQueriesAgainstInfluxDB(t *testing.T) {
	client, err := influxdb3.New(influxdb3.ClientConfig{
		Host: integrationInfluxDB.host, Token: integrationInfluxDB.token, Database: integrationInfluxDB.database,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	now := time.Now().UTC()
	identity := fmt.Sprintf("%d", now.UnixNano())
	aircraftID := "aircraft-api-samples-" + identity
	flightID := "flight-api-samples-" + identity
	points := make([]*influxdb3.Point, 0, 4)
	for index := 1; index <= 4; index++ {
		points = append(points, influxdb3.NewPoint(tableName, map[string]string{
			"agent_id":       "agent-samples",
			"frame_id":       fmt.Sprintf("sample-frame-%s-%d", identity, index),
			"message_name":   messageName,
			"schema_version": "1",
		}, map[string]interface{}{
			"aircraft_id":      aircraftID,
			"flight_id":        flightID,
			"operator_id":      "operator-samples",
			"intent_id":        "intent-samples",
			"intent_version":   uint64(3),
			"relay_id":         "relay-samples",
			"session_id":       "session-samples",
			"timestamp_source": "agent_capture",
			"latitude_deg":     41.0 + float64(index)/100,
			"longitude_deg":    -87.0 - float64(index)/100,
			"altitude_msl_m":   100.0 + float64(index),
			"groundspeed_mps":  float64(index),
			"heading_deg":      float64(index * 10),
		}, now.Add(time.Duration(index-5)*time.Second)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.WritePoints(ctx, points); err != nil {
		t.Fatalf("write telemetry samples: %v", err)
	}
	store, err := New(integrationInfluxDB.host, integrationInfluxDB.token, integrationInfluxDB.database, defaultLatestLookback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close telemetry store: %v", err)
		}
	})

	for {
		latest, latestErr := store.GetLatestSample(ctx, aircraftID)
		aircraftSamples, aircraftErr := store.QueryAircraftSamples(ctx, aircraftID, 2)
		flightSamples, flightErr := store.QueryFlightSamples(ctx, flightID, 2)
		if latestErr == nil && len(aircraftSamples) == 2 && aircraftErr == nil && len(flightSamples) == 2 && flightErr == nil {
			if latest.ID != fmt.Sprintf("sample-frame-%s-4", identity) {
				t.Fatalf("latest sample = %#v, want fourth frame", latest)
			}
			assertSampleWindow(t, "latest aircraft window", aircraftSamples, []int{3, 4}, identity, aircraftID, flightID)
			assertSampleWindow(t, "earliest flight window", flightSamples, []int{1, 2}, identity, aircraftID, flightID)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("telemetry samples did not become queryable: %v (latest=%v aircraft=%v/%d flight=%v/%d)",
				ctx.Err(), latestErr, aircraftErr, len(aircraftSamples), flightErr, len(flightSamples))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func assertSampleWindow(t *testing.T, name string, samples []domain.TelemetrySample, indexes []int, identity, aircraftID, flightID string) {
	t.Helper()
	for position, index := range indexes {
		sample := samples[position]
		wantID := fmt.Sprintf("sample-frame-%s-%d", identity, index)
		if sample.ID != wantID || sample.AircraftID != aircraftID || sample.FlightID != flightID {
			t.Fatalf("%s[%d] = %#v, want id=%q aircraft=%q flight=%q", name, position, sample, wantID, aircraftID, flightID)
		}
		if sample.Latitude != 41.0+float64(index)/100 || sample.Longitude != -87.0-float64(index)/100 || sample.AltitudeM != 100.0+float64(index) || sample.VelocityMPS != float64(index) || sample.HeadingDeg != float64(index*10) {
			t.Fatalf("%s[%d] decoded fields = %#v", name, position, sample)
		}
		if sample.OperatorID != "operator-samples" || sample.IntentID != "intent-samples" || sample.IntentVersion != 3 {
			t.Fatalf("%s[%d] operation context = %#v", name, position, sample)
		}
		if position > 0 && sample.RecordedAt.Before(samples[position-1].RecordedAt) {
			t.Fatalf("%s is not chronological: %#v", name, samples)
		}
	}
}
