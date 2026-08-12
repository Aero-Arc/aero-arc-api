//go:build integration

package influxdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	influxdb3 "github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
)

func TestLiveAircraftStateAgainstInfluxDB(t *testing.T) {
	host := os.Getenv("AERO_API_TEST_INFLUXDB_HOST")
	token := os.Getenv("AERO_API_TEST_INFLUXDB_TOKEN")
	database := os.Getenv("AERO_API_TEST_INFLUXDB_DATABASE")
	if host == "" || token == "" || database == "" {
		t.Skip("AERO_API_TEST_INFLUXDB_HOST, _TOKEN, and _DATABASE are required")
	}

	client, err := influxdb3.New(influxdb3.ClientConfig{Host: host, Token: token, Database: database})
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
