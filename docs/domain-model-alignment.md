# Domain Model Alignment

This API models Aero Arc as BVLOS-readiness and compliance evidence
infrastructure. The durable domain is intentionally broader than fleet CRUD: it
needs to prove aircraft identity, accepted configuration, personnel
responsibility, operational intent, preflight evidence, maintenance state,
telemetry-derived conformance, reportability decisions, and exportable records.

The model follows the source-of-truth split from the distributed architecture:

- Registry: live topology, liveness, relay placement, and last-known live state.
- Durable store: aircraft, batteries, operating limits, operational intent,
  preflight checks, maintenance, evidence, conformance summaries, reportability,
  and personnel records.
- Telemetry store: high-volume time-series samples.
- Replay store: replay manifests and object/chunk references.

## Domain Groups

### Aircraft And Acceptance

`Aircraft` is a compliance object, not only a telemetry source. It tracks
registration, serial number, model/manufacturer, acceptance status, Remote ID
state, and software/configuration versions.

`AircraftOperatingProfile` stores structured limits that are commonly queried
by dashboards and preflight checks: max groundspeed, max takeoff weight,
altitude ceiling, weather envelope, DAA capability, lighting, PNT integrity,
and manufacturer limit references.

`OperatingLimit` remains available for additional generic limits that do not
deserve first-class columns yet.

### Operations

`OperationalIntent` is the atomic operation object. It carries the operation
name, use case, authorization path, population category, planned time window,
route summary, altitude band, assigned roles, and whether conformance monitoring
is required.

This should be the common join point for preflight checks, conformance,
evidence packages, reportability reviews, and flight records.

### Preflight And Evidence

`PreflightCheck` records auditable checks such as weather, NOTAM, airspace,
population category, obstacles, battery reserve, maintenance release, Remote ID,
personnel, and cybersecurity/access readiness.

`EvidenceRecord` is the durable ledger for exportable artifacts: preflight
packages, flight records, maintenance releases, conformance records,
reportability reviews, security reports, and monthly exports.

### Maintenance And Batteries

`Battery`, `BatteryInstallation`, and `MaintenanceEvent` cover physical battery
lifecycle, aircraft installation, mechanical irregularities, due dates, owners,
corrective actions, return-to-service, and dispatch-blocking maintenance state.

Telemetry-derived battery voltage/current trends should stay in the telemetry
store; lifecycle state of health and cycle count belong in durable storage.

### Conformance And Reporting

`ConformanceEvent` records individual deviations or alerts tied to a flight and
optionally an intent.

`ConformanceSummary` stores the operation/flight-level result: required,
active, complete, degraded, blocked, score, alert count, and reportability
status.

`ReportabilityReview` models the decision workflow for SDR/security/emergency
deviation/damage/unauthorized-area style triggers.

### Personnel And Security

`OperationsPersonnel` records operational roles, qualification state, security
assessment state, recent experience, duty start, and rest timestamps. It is the
durable anchor for operations supervisor and flight coordinator assignments on
intents.

## Enum Strategy

Status-like fields use typed Go string enums in `internal/domain/models.go`.
This gives readable JSON and compile-time clarity without hiding values behind
magic strings.

For Postgres, use one of two patterns:

1. Native Postgres `ENUM` for stable values that are unlikely to churn.
2. `TEXT NOT NULL CHECK (...)` for values likely to change while the Part 108
   rule and product language mature.

Recommended first implementation:

- Use native enums for stable internal workflow states:
  `severity`, `readiness_status`, `flight_status`, `intent_status`,
  `maintenance_status`, `evidence_status`.
- Use `TEXT CHECK` for externally shaped or evolving regulatory language:
  `authorization_path`, `population_category`, `preflight_check_category`,
  `evidence_record_type`, `reportability_status`, `conformance_status`.

If TiDB becomes a target, prefer `VARCHAR` plus `CHECK` constraints or lookup
tables for maximum portability.

## Efficient Table Shape

Use narrow relational columns for frequent filters and joins. Avoid JSONB as
the primary storage shape for core compliance records. JSONB is appropriate for
raw source payloads, external snapshots, or uncommon extension data.

Recommended indexes:

- `aircraft(agent_id)`
- `aircraft(registration)`
- `aircraft(acceptance_status, status)`
- `batteries(serial_number)`
- `battery_installations(aircraft_id, removed_at)`
- `maintenance_events(aircraft_id, status, due_at)`
- `operational_intents(aircraft_id, status, planned_start_at)`
- `preflight_checks(intent_id, category, status)`
- `flight_records(aircraft_id, started_at DESC)`
- `flight_records(intent_id)`
- `conformance_events(flight_id, occurred_at)`
- `conformance_summaries(intent_id)`
- `evidence_records(intent_id, type, status)`
- `reportability_reviews(intent_id, status)`
- `operations_personnel(role, qualification_status)`

## Postgres Sketch

```sql
CREATE TYPE readiness_status AS ENUM (
  'ready', 'review', 'warning', 'blocked', 'unknown'
);

CREATE TYPE severity AS ENUM (
  'info', 'advisory', 'warning', 'critical'
);

CREATE TABLE aircraft (
  id text PRIMARY KEY,
  agent_id text,
  tail_number text NOT NULL,
  registration text,
  serial_number text,
  name text NOT NULL,
  model text NOT NULL,
  manufacturer text NOT NULL,
  status text NOT NULL CHECK (status IN ('active', 'inactive', 'maintenance', 'review')),
  acceptance_status text NOT NULL CHECK (acceptance_status IN ('draft', 'review', 'accepted', 'rejected', 'expired')),
  remote_id_serial text,
  remote_id_status text NOT NULL CHECK (remote_id_status IN ('unknown', 'broadcasting', 'offline', 'degraded')),
  config_version text,
  software_version text,
  hardware_version text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE operational_intents (
  id text PRIMARY KEY,
  aircraft_id text NOT NULL REFERENCES aircraft(id),
  name text NOT NULL,
  summary text NOT NULL,
  use_case text,
  authorization_path text NOT NULL,
  population_category text NOT NULL,
  status text NOT NULL,
  conformance_required boolean NOT NULL DEFAULT false,
  operating_area_id text,
  route_summary text,
  planned_start_at timestamptz NOT NULL,
  planned_end_at timestamptz NOT NULL,
  min_altitude_ft_agl double precision,
  max_altitude_ft_agl double precision,
  supervisor_id text,
  flight_coordinator_id text,
  submitted_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE evidence_records (
  id text PRIMARY KEY,
  type text NOT NULL,
  intent_id text REFERENCES operational_intents(id),
  flight_id text,
  aircraft_id text REFERENCES aircraft(id),
  status text NOT NULL,
  title text NOT NULL,
  summary text,
  object_uri text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
```

## Implementation Notes

The current memory durable store now supports the expanded records so service
and endpoint work can proceed before Postgres exists.

Next useful backend step: add a Postgres package under
`internal/store/durable/postgres` and map these domain types into normalized
tables with migrations. Keep DTO shaping in the API/service layer; do not let
the frontend talk directly to storage-specific shapes.
