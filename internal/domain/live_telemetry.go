package domain

import "time"

type DataFreshness string

const (
	DataFreshnessFresh       DataFreshness = "fresh"
	DataFreshnessStale       DataFreshness = "stale"
	DataFreshnessMissing     DataFreshness = "missing"
	DataFreshnessUnavailable DataFreshness = "unavailable"
)

type TelemetryObservation struct {
	Status          DataFreshness `json:"status"`
	RecordedAt      time.Time     `json:"recorded_at"`
	FrameID         string        `json:"frame_id,omitempty"`
	RelayID         string        `json:"relay_id,omitempty"`
	SessionID       string        `json:"session_id,omitempty"`
	TimestampSource string        `json:"timestamp_source,omitempty"`
	OperatorID      string        `json:"-"`
	IntentID        string        `json:"-"`
	IntentVersion   int           `json:"-"`
	FlightID        string        `json:"-"`
}

type PositionTelemetry struct {
	TelemetryObservation
	LatitudeDeg       float64  `json:"latitude_deg"`
	LongitudeDeg      float64  `json:"longitude_deg"`
	AltitudeMSLM      *float64 `json:"altitude_msl_m,omitempty"`
	RelativeAltitudeM *float64 `json:"relative_altitude_m,omitempty"`
	VelocityNorthMPS  *float64 `json:"velocity_north_mps,omitempty"`
	VelocityEastMPS   *float64 `json:"velocity_east_mps,omitempty"`
	VelocityDownMPS   *float64 `json:"velocity_down_mps,omitempty"`
	GroundspeedMPS    *float64 `json:"groundspeed_mps,omitempty"`
	HeadingDeg        *float64 `json:"heading_deg,omitempty"`
}

type BatteryTelemetry struct {
	TelemetryObservation
	BatteryID             uint64   `json:"battery_id"`
	BatteryFunction       string   `json:"battery_function,omitempty"`
	BatteryType           string   `json:"battery_type,omitempty"`
	BatteryChargeState    string   `json:"battery_charge_state,omitempty"`
	BatteryMode           string   `json:"battery_mode,omitempty"`
	BatteryTemperatureC   *float64 `json:"battery_temperature_c,omitempty"`
	BatteryVoltageV       *float64 `json:"battery_voltage_v,omitempty"`
	BatteryCurrentA       *float64 `json:"battery_current_a,omitempty"`
	BatteryConsumedMAH    *int64   `json:"battery_consumed_mah,omitempty"`
	BatteryConsumedWH     *float64 `json:"battery_consumed_wh,omitempty"`
	BatteryRemainingPct   *float64 `json:"battery_remaining_pct,omitempty"`
	BatteryTimeRemainingS *int64   `json:"battery_time_remaining_s,omitempty"`
}

type VehicleTelemetry struct {
	TelemetryObservation
	VehicleType    string  `json:"vehicle_type,omitempty"`
	AutopilotType  string  `json:"autopilot_type,omitempty"`
	BaseMode       string  `json:"base_mode,omitempty"`
	CustomMode     *uint64 `json:"custom_mode,omitempty"`
	SystemStatus   string  `json:"system_status,omitempty"`
	MAVLinkVersion *uint64 `json:"mavlink_version,omitempty"`
}

type SystemTelemetry struct {
	TelemetryObservation
	MainloopLoadPct          *float64 `json:"mainloop_load_pct,omitempty"`
	CommunicationDropRatePct *float64 `json:"communication_drop_rate_pct,omitempty"`
	CommunicationErrorCount  *uint64  `json:"communication_error_count,omitempty"`
	AutopilotErrorCount1     *uint64  `json:"autopilot_error_count_1,omitempty"`
	AutopilotErrorCount2     *uint64  `json:"autopilot_error_count_2,omitempty"`
	AutopilotErrorCount3     *uint64  `json:"autopilot_error_count_3,omitempty"`
	AutopilotErrorCount4     *uint64  `json:"autopilot_error_count_4,omitempty"`
	SensorsPresent           string   `json:"sensors_present,omitempty"`
	SensorsEnabled           string   `json:"sensors_enabled,omitempty"`
	SensorsHealth            string   `json:"sensors_health,omitempty"`
	SensorsPresentExtended   string   `json:"sensors_present_extended,omitempty"`
	SensorsEnabledExtended   string   `json:"sensors_enabled_extended,omitempty"`
	SensorsHealthExtended    string   `json:"sensors_health_extended,omitempty"`
}

type HUDTelemetry struct {
	TelemetryObservation
	AirspeedMPS    *float64 `json:"airspeed_mps,omitempty"`
	GroundspeedMPS *float64 `json:"groundspeed_mps,omitempty"`
	HeadingDeg     *float64 `json:"heading_deg,omitempty"`
	ThrottlePct    *float64 `json:"throttle_pct,omitempty"`
	AltitudeMSLM   *float64 `json:"altitude_msl_m,omitempty"`
	ClimbRateMPS   *float64 `json:"climb_rate_mps,omitempty"`
}

type ExtendedStateTelemetry struct {
	TelemetryObservation
	VTOLState   string `json:"vtol_state,omitempty"`
	LandedState string `json:"landed_state,omitempty"`
}

type GPSTelemetry struct {
	TelemetryObservation
	FixType             string   `json:"gps_fix_type,omitempty"`
	LatitudeDeg         *float64 `json:"gps_latitude_deg,omitempty"`
	LongitudeDeg        *float64 `json:"gps_longitude_deg,omitempty"`
	AltitudeMSLM        *float64 `json:"gps_altitude_msl_m,omitempty"`
	AltitudeEllipsoidM  *float64 `json:"gps_altitude_ellipsoid_m,omitempty"`
	HDOP                *float64 `json:"gps_hdop,omitempty"`
	VDOP                *float64 `json:"gps_vdop,omitempty"`
	GroundspeedMPS      *float64 `json:"gps_groundspeed_mps,omitempty"`
	CourseOverGroundDeg *float64 `json:"gps_course_over_ground_deg,omitempty"`
	SatellitesVisible   *uint64  `json:"gps_satellites_visible,omitempty"`
	HorizontalAccuracyM *float64 `json:"gps_horizontal_accuracy_m,omitempty"`
	VerticalAccuracyM   *float64 `json:"gps_vertical_accuracy_m,omitempty"`
	SpeedAccuracyMPS    *float64 `json:"gps_speed_accuracy_mps,omitempty"`
	HeadingAccuracyDeg  *float64 `json:"gps_heading_accuracy_deg,omitempty"`
	YawDeg              *float64 `json:"gps_yaw_deg,omitempty"`
}

type AircraftTelemetryState struct {
	Status         DataFreshness           `json:"status"`
	LastObservedAt *time.Time              `json:"last_observed_at,omitempty"`
	Position       *PositionTelemetry      `json:"position,omitempty"`
	Battery        *BatteryTelemetry       `json:"battery,omitempty"`
	Vehicle        *VehicleTelemetry       `json:"vehicle,omitempty"`
	System         *SystemTelemetry        `json:"system,omitempty"`
	HUD            *HUDTelemetry           `json:"hud,omitempty"`
	ExtendedState  *ExtendedStateTelemetry `json:"extended_state,omitempty"`
	GPS            *GPSTelemetry           `json:"gps,omitempty"`
}
