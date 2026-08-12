# UI Data Contract

This document defines how Aero Arc UI surfaces consume the `aero-arc-api`
product model. Frontends must call `aero-arc-api` only; they must not call
registry, relay, telemetry store, replay store, durable DB, or agent services
directly.

## Naming

- Use `aircraft` for durable vehicle identity and configuration.
- Use `agent` for the live software identity associated with an aircraft.
- Use `relay` for telemetry/control-plane routing placement.
- Use `operational_intent` for planned or authorized operations.
- Use `conformance_event` for operational envelope deviations.
- Use `evidence_record` for audit, export, and review artifacts.
- Use `readiness` for fly/no-fly dashboard status.

API JSON uses snake_case. Flutter/Dart DTO fields may use camelCase, but their
`fromJson` and `toJson` methods must map the exact API keys.

## Dashboard To API Mapping

| UI surface | API endpoint | Response model | Backend entities | Source-of-truth notes | Status |
| --- | --- | --- | --- | --- | --- |
| Overview | `GET /api/v1/overview` | `OverviewDashboard` | `AircraftDashboard`, `OperationalIntent`, `EvidenceRecord`, `ReportabilityReview`, `DashboardMetric` | Composite API response from durable records plus live aircraft dashboards | Existing API, UI not wired |
| Operations | `GET /api/v1/operations` | `OperationsDashboard` | `OperationalIntent`, `ConformanceSummary`, `DashboardMetric` | Durable operational records and conformance summaries | Existing API, UI mostly absent |
| Preflight | `GET /api/v1/preflight` | `PreflightDashboard` | `PreflightCheck`, `DashboardMetric` | Durable/auditable preflight records | Existing API, UI absent |
| Conformance | `GET /api/v1/conformance` | `ConformanceDashboard` | `ConformanceSummary`, `ConformanceEvent`, `DashboardMetric` | Durable conformance records, often telemetry-derived upstream | Existing API, UI placeholder only |
| Maintenance | `GET /api/v1/maintenance` | `MaintenanceDashboard` | `MaintenanceEvent`, `Battery`, `DashboardMetric` | Durable maintenance and battery lifecycle records | Existing API, UI partial |
| Records | `GET /api/v1/records` | `RecordsDashboard` | `EvidenceRecord`, `ReportabilityReview`, `DashboardMetric` | Durable evidence and reportability workflow records | Existing API, UI absent |
| Fleet aircraft list | `GET /api/v1/aircraft` | `{ "aircraft": AircraftDashboard[] }` | `Aircraft`, `Battery`, `MaintenanceEvent`, `TelemetrySample`, `LiveAircraftState`, `Readiness` | Durable aircraft composed with active battery, latest telemetry, and registry live state | First UI slice |
| Aircraft detail | `GET /api/v1/aircraft/{aircraft_id}` | `AircraftDashboard` | `Aircraft`, `Battery`, `MaintenanceEvent`, `TelemetrySample`, `LiveAircraftState`, `Readiness` | Bare dashboard object for one aircraft | First UI slice |
| Aircraft live state | `GET /api/v1/aircraft/{aircraft_id}/state` | `AircraftLiveState` | Registry agent/placement plus independent InfluxDB message groups | Focused live-state response; missing groups are omitted | Implemented |
| Replay | `GET /api/v1/flights/{flight_id}/replay` | `ReplayResponse` | `FlightRecord`, `ReplayManifest`, `TelemetrySample`, `ConformanceEvent` | Durable flight, replay store manifest, telemetry samples | Existing API, UI absent |

## Entity To UI Mapping

| API entity | Source class | UI use | Needs Flutter DTO | Currently mocked |
| --- | --- | --- | --- | --- |
| `Aircraft` | Durable | Fleet identity, name, tail number, registration, model, agent association | Yes | Yes |
| `Battery` | Durable | Active battery SOH, cycle count, maintenance state | Yes | Yes |
| `BatteryInstallation` | Durable | Active pack association; currently resolved by API into `active_battery` | Later | No direct UI |
| `MaintenanceEvent` | Durable | Maintenance blockers and warnings | Yes | Partial |
| `AircraftOperatingProfile` | Durable | Operating constraints and aircraft capability summary | Later | No |
| `OperatingLimit` | Durable | Limit badges and constraint detail | Later | No |
| `OperationalIntent` | Durable | Planned/active operation state | Later | Mission-like labels only |
| `PreflightCheck` | Durable | Readiness and preflight status | Later | No |
| `FlightRecord` | Durable | Flight history and replay entry points | Later | No |
| `ConformanceEvent` | Durable, telemetry-derived | Events and deviation timeline | Later | Generic events only |
| `ConformanceSummary` | Durable | Compliance/conformance status | Later | No |
| `EvidenceRecord` | Durable | Records/evidence exports | Later | No |
| `ReportabilityReview` | Durable | Reportability status and review workflow | Later | No |
| `OperationsPersonnel` | Durable | Assigned supervisor/coordinator display | Later | No |
| `TelemetrySample` | Telemetry | Latest telemetry and replay samples | Yes for aircraft slice | Yes |
| `ReplayManifest` | Replay | Replay asset/chunk lookup | Later | No |
| `LiveAircraftState` | Registry/live via API | Connected/offline state, agent ID, relay ID, heartbeat | Yes | Yes |
| Dashboard models | Composite API DTOs | Cards, metrics, dashboard sections | Yes as needed | Yes |

## AircraftDashboard Vertical Slice

The first frontend slice uses the existing aircraft endpoints exactly as they
are implemented today:

- `GET /api/v1/aircraft` returns an envelope:
  `{ "aircraft": [AircraftDashboard...] }`
- `GET /api/v1/aircraft/{aircraft_id}` returns a bare `AircraftDashboard`.

The Flutter DTOs for this slice should mirror:

- `aircraft`
- `active_battery`
- `maintenance_events`
- `latest_telemetry`
- `telemetry`
- `live_state`
- `live_state_available`
- `readiness`
- `current_intent`

UI-only labels, colors, formatted dates, maintenance summaries, and telemetry
summaries must live in frontend view-model/helper code, not in API DTOs.

## Live Aircraft Contract

`GET /api/v1/aircraft/{aircraft_id}/state` and each entry in
`OperationsDashboard.live_aircraft` contain durable identity plus:

- `connection`: registry status, heartbeat, and placement. Status is
  `connected`, `stale`, `offline`, `unmapped`, or `unavailable`.
- `telemetry`: aggregate status and nullable `position`, `battery`, `vehicle`,
  `system`, `hud`, `extended_state`, and `gps` observations.
- Every telemetry observation has its own `recorded_at` and `status`; consumers
  must not assume independently emitted MAVLink messages share a sample time.
- Optional numeric fields distinguish an absent InfluxDB column from a valid
  zero. UI DTOs should therefore use nullable numbers for optional fields.

By default a registry heartbeat is connected through 30 seconds and a
telemetry observation is fresh through 15 seconds. These server-side policies
are configurable. The aggregate telemetry status describes its newest
observation; render group status when showing a specific value.

## Relay To InfluxDB Telemetry Contract

The source of truth for writes is the `Aero-Arc/aero-arc-relay` repository:
`internal/telemetrywriter/influx/point.go` owns the InfluxDB point layout,
`internal/telemetrynormalize/normalizers.go` owns normalization, and
`docs/telemetry-normalization-fields-v1.md` owns conversions and validity
ranges. The API read side is implemented by
`internal/store/telemetry/influxdb/store.go`. A change to any name or type in
this section must update and test both repositories.

Relay writes the InfluxDB measurement `aircraft_telemetry`. Each point is one
normalized MAVLink message, not a complete aircraft snapshot. InfluxDB `time`
is the normalized event time selected by Relay; API exposes it as that group's
`recorded_at` and selects the newest row independently for each
`aircraft_id`/`message_name` pair. Relay chooses a validated device UTC time
first, then agent capture time, then relay receive time; `timestamp_source` is
respectively `device_utc`, `agent_capture`, or `relay_receive`. A boot-relative
device timestamp is never treated directly as Unix time.

### Tags and common columns

| Name | Influx kind/type | Required | Meaning |
| --- | --- | --- | --- |
| `agent_id` | tag, string | yes | Agent that produced the frame. |
| `frame_id` | tag, string | yes | Stable idempotency key for one agent WAL frame; unchanged by retry or reconnect. |
| `message_name` | tag, string | yes | Canonical normalized message group listed below. |
| `schema_version` | tag, decimal string | yes | Normalized record schema; the current contract is `"1"`. |
| `time` | InfluxDB timestamp | yes | Normalized event time used for latest-row ordering and exposed by API as `recorded_at`. |
| `relay_id` | field, string | yes | Relay that normalized and wrote the frame. |
| `aircraft_id` | field, string | required for aircraft API reads | Durable aircraft assignment. Authenticated but unmapped agent points may omit it and are intentionally invisible to aircraft queries. |
| `session_id` | field, string | yes | Opaque Relay registration/connection lifecycle ID. It may change after registration or reconnect and is not an idempotency key. |
| `operator_id`, `flight_id`, `intent_id` | field, string | no | Operation context active when the frame was normalized. |
| `intent_version` | field, uint64 | no | Omitted when zero or when no intent context exists. |
| `wal_sequence`, `message_id` | field, uint64 | yes | Durable agent sequence and MAVLink message ID. |
| `dialect`, `timestamp_source` | field, string | yes | MAVLink dialect and the event-time selection basis. |
| `relay_time_ns` | field, int64 | yes | Relay receive time in Unix nanoseconds; it is metadata, not InfluxDB `time`. |
| `agent_capture_time_ns` | field, int64 | no | Agent capture time in Unix nanoseconds when usable. |
| `device_time_value`, `device_time_unit`, `device_time_basis` | fields, uint64/string/string | no | Message-provided device time and its interpretation. |
| `mavlink_system_id`, `mavlink_component_id` | field, uint64 | no | MAVLink source identifiers when supplied. |

The API currently returns `frame_id`, `relay_id`, `session_id`, and
`timestamp_source` on each observation. It uses `aircraft_id` to select rows;
operation-context columns support legacy/replay models but are not exposed on
the live observation JSON.

### Message groups read by the API

All fields below are InfluxDB fields. `float64`, `uint64`, and `int64` describe
the normalized value type written by Relay. Except where marked required, a
field is optional and omitted when the source value is absent, a MAVLink
sentinel, out of range, or not representable.

| `message_name` / API group | Required normalized fields | Optional normalized fields |
| --- | --- | --- |
| `global_position_int` / `position` | `latitude_deg` float64; `longitude_deg` float64 | `altitude_msl_m`, `relative_altitude_m`, `velocity_north_mps`, `velocity_east_mps`, `velocity_down_mps`, `groundspeed_mps`, `heading_deg` (float64) |
| `battery_status` / `battery` | `battery_id` uint64 | `battery_function`, `battery_type`, `battery_charge_state`, `battery_mode` (string); `battery_temperature_c`, `battery_voltage_v`, `battery_current_a`, `battery_consumed_wh`, `battery_remaining_pct` (float64); `battery_consumed_mah`, `battery_time_remaining_s` (int64) |
| `heartbeat` / `vehicle` | none | `vehicle_type`, `autopilot_type`, `base_mode`, `system_status` (string); `custom_mode`, `mavlink_version` (uint64) |
| `sys_status` / `system` | none | `mainloop_load_pct`, `communication_drop_rate_pct` (float64); `communication_error_count`, `autopilot_error_count_1` through `_4` (uint64); `sensors_present`, `sensors_enabled`, `sensors_health` and their `_extended` forms (string) |
| `vfr_hud` / `hud` | none | `airspeed_mps`, `groundspeed_mps`, `heading_deg`, `throttle_pct`, `altitude_msl_m`, `climb_rate_mps` (float64) |
| `extended_sys_state` / `extended_state` | none | `vtol_state`, `landed_state` (string) |
| `gps_raw_int` / `gps` | none | `gps_fix_type` (string); `gps_latitude_deg`, `gps_longitude_deg`, `gps_altitude_msl_m`, `gps_altitude_ellipsoid_m`, `gps_hdop`, `gps_vdop`, `gps_groundspeed_mps`, `gps_course_over_ground_deg`, `gps_horizontal_accuracy_m`, `gps_vertical_accuracy_m`, `gps_speed_accuracy_mps`, `gps_heading_accuracy_deg`, `gps_yaw_deg` (float64); `gps_satellites_visible` (uint64) |

Relay also normalizes `system_time`, but `GetLatestAircraftStates` does not
currently query or expose it. Adding a message group requires adding it to the
API query allowlist, decoder, domain/JSON contract, and frontend model rather
than relying on `SELECT *` alone.

Required fields are validated as genuinely numeric: numeric zero is valid,
while missing, nonnumeric, NaN, or infinite required values make only that
observation malformed. Optional numeric fields are nullable in API JSON so an
omitted column is never coerced to zero. A malformed row is isolated to its
aircraft/message group; independently sampled valid groups remain available.

## Known Gaps

- `AircraftDashboard.operating_profile` exists but is not currently populated
  by `FleetService`.
- `LiveAircraftState` does not include location; latest position comes from
  `telemetry.position` (with `latest_telemetry` retained for compatibility).
- Aircraft cards do not yet receive latest preflight or conformance summaries
  directly; consumers must use the broader dashboard endpoints for now.
- `DashboardMetric` uses display labels and string values, not stable metric
  IDs, so UI logic should not depend on metric labels as identifiers.
