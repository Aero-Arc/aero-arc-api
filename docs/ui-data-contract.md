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

## Known Gaps

- `AircraftDashboard.operating_profile` exists but is not currently populated
  by `FleetService`.
- `LiveAircraftState` does not include location; latest position comes from
  `telemetry.position` (with `latest_telemetry` retained for compatibility).
- Aircraft cards do not yet receive latest preflight or conformance summaries
  directly; consumers must use the broader dashboard endpoints for now.
- `DashboardMetric` uses display labels and string values, not stable metric
  IDs, so UI logic should not depend on metric labels as identifiers.
