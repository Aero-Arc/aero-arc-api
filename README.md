# aero-arc-api

`aero-arc-api` is the public, UI-facing API layer for Aero Arc. It acts as the
backend-for-frontend for fleet dashboards, flight records, and replay views.

The frontend should talk only to this service. It should not call
`aero-arc-registry`, telemetry stores, replay storage, or durable databases
directly.

## Source-of-Truth Boundaries

- Registry: live topology, liveness, relay placement, and last-known connected
  state.
- Durable store: aircraft, batteries, battery installations, maintenance events,
  flight records, operational intents and volumes, conflict findings, and
  conformance events.
- PostGIS indexes: transactionally maintained indexes over authoritative
  operational volumes for local conflict-candidate discovery.
- Telemetry store: queryable time-series telemetry samples.
- Replay store: raw replay manifests and log chunk locations.

Raw high-frequency telemetry ingestion belongs on the relay sink path, not
through this API service.

## Run Locally

The scaffold defaults to in-memory stores and an in-memory registry client.

```bash
go run ./cmd/aero-arc-api start
```

For local UI development, start the API with seeded demo dashboard data:

```bash
go run ./cmd/aero-arc-api start --seed demo
```

or:

```bash
make run-demo
```

### Docker Compose

The Compose stack runs the API and PostGIS on the same bridge network. The API
container starts only after PostgreSQL passes its readiness check.

```bash
cp .env.example .env
docker compose up --build
```

The API is available at `http://localhost:8080`, and PostgreSQL is exposed to
the host at `localhost:5432`. Containers on the Compose network address the
database as `postgis:5432`. The host ports and local development credentials
can be changed in `.env`. These defaults are for local development only; use
managed secrets and restrict database exposure in shared environments.

Database data is retained in the named `postgis-data` volume. To stop the
containers without deleting the data:

```bash
docker compose down
```

Compose uses PostgreSQL with PostGIS as the authoritative store for operational
intents, volumes, and conflict findings. Spatial indexes are maintained by
PostgreSQL in the same transactions as volume writes. Other durable domain
groups remain in memory during this vertical slice.

Configuration is available through flags or environment variables:

```bash
go run ./cmd/aero-arc-api start \
  --addr :8080 \
  --durable-store memory \
  --airspace-provider local \
  --telemetry-store memory \
  --replay-store memory \
  --registry-mode memory \
  --seed demo \
  --debug
```

```bash
AERO_API_ADDR=:8080
AERO_API_DURABLE_STORE=memory
AERO_API_AIRSPACE_PROVIDERS=local
AERO_API_TELEMETRY_STORE=memory
AERO_API_REPLAY_STORE=memory
AERO_API_REGISTRY_MODE=memory
AERO_API_REGISTRY_ADDR=localhost:50051
AERO_API_REGISTRY_DIAL_TIMEOUT=5s
AERO_API_REGISTRY_FRESHNESS=30s
AERO_API_TELEMETRY_FRESHNESS=15s
AERO_API_TELEMETRY_LATEST_LOOKBACK=5m
AERO_API_REQUEST_TIMEOUT=3s
AERO_API_SEED=demo
AERO_API_DEBUG=true
```

To read normalized live telemetry from InfluxDB 3 Core, set
`AERO_API_TELEMETRY_STORE=influxdb` together with
`AERO_API_INFLUXDB_HOST`, `AERO_API_INFLUXDB_TOKEN`, and
`AERO_API_INFLUXDB_DATABASE`. Demo seeding remains available only with the
memory telemetry store.

`AERO_API_REGISTRY_MODE=grpc` connects to the real `aero-arc-registry` gRPC
service. Durable and replay stores still run in `memory` mode in this scaffold.

### Live aircraft state

The relay writes independent normalized MAVLink observations to the
`aircraft_telemetry` InfluxDB measurement. The API queries the latest
`global_position_int`, `battery_status`, `heartbeat`, `sys_status`, `vfr_hud`,
`extended_sys_state`, and `gps_raw_int` record for each aircraft and composes
them with registry liveness and relay placement:

```bash
curl http://localhost:8080/api/v1/aircraft/aircraft-1/state
```

The response contains `connection` and `telemetry`. Every present telemetry
group has its own `recorded_at` and `status` (`fresh` or `stale`); absent groups
are omitted. The aggregate telemetry status is `fresh` when the newest group is
within the configured window, `stale` otherwise, `missing` when no observations
exist, and `unavailable` when the telemetry query fails. Connection status is
`connected`, `stale`, `offline`, `unmapped`, or `unavailable`.

Aircraft-to-agent association is explicit: `aircraft.agent_id` must match the
registry agent ID. The API never substitutes `aircraft.id`. Registry heartbeats
are fresh for 30 seconds and telemetry for 15 seconds by default; change these
with `AERO_API_REGISTRY_FRESHNESS` and `AERO_API_TELEMETRY_FRESHNESS`.
Live-state InfluxDB queries rank only observations inside the latest lookback
(five minutes by default) so retained flight history is not scanned on every
Operations refresh. Configure it with `AERO_API_TELEMETRY_LATEST_LOOKBACK`; it
must be at least the telemetry freshness window. Replay and aircraft-history
queries retain their independent explicit limits and are not clipped by this
live-state policy.

`GET /api/v1/operations` includes the same composites in `live_aircraft`, and
aircraft list/detail/map responses retain their legacy `live_state` and
`latest_telemetry` fields while also exposing the grouped `telemetry` object.

### Integration tests

Integration tests use Testcontainers and own their PostGIS and InfluxDB
dependencies by default. With Docker available, the suite pulls the pinned
`postgis/postgis:14-3.5-alpine` and `influxdb:3.10.3-core` images, assigns
dynamic host ports, creates the test databases, waits for readiness, and
removes the containers after the owning package finishes:

```bash
go test -tags=integration ./...
```

The PostGIS and InfluxDB packages each start at most one container and share it
across that package's tests. A failing package prints its dependency container
logs before cleanup. The bufconn and `httptest` integrations stay in-process;
DSS tests remain opt-in and use their existing external configuration.

Externally managed services can replace either test-owned dependency. Set the
PostGIS URL to bypass its container:

```bash
AERO_API_TEST_POSTGIS_URL='postgres://aero_arc_test:aero_arc_test@localhost:5432/aero_arc_test?sslmode=disable' \
go test -tags=integration ./internal/store/durable/postgres
```

Set all three InfluxDB values together to bypass its container. The external
database must already exist:

```bash
AERO_API_TEST_INFLUXDB_HOST=http://localhost:8181 \
AERO_API_TEST_INFLUXDB_TOKEN=replace-me \
AERO_API_TEST_INFLUXDB_DATABASE=aero_arc_test \
go test -tags=integration ./internal/store/telemetry/influxdb
```

## Deconfliction Read, Check, and Publication Slice

The [DSS operational-intent publication guide](docs/deconfliction-publication.md)
documents the lifecycle, durable state model, concurrency invariants, ambiguous
remote-outcome recovery, peer notification semantics, and current production
limits. This section focuses on configuration and the public API surface.

Set `AERO_API_DURABLE_STORE=postgres` and `AERO_API_DATABASE_URL` to persist
the deconfliction slice in PostgreSQL and use PostGIS for local spatial
candidate discovery. The required schema is initialized automatically.

Airspace sources are explicit and composable. `local` queries authoritative
operational volumes through PostGIS. `interuss` queries
DSS references for each submitted WGS84 volume and fetches full details from
each managing USS. Peer failures produce an indeterminate, blocking finding
while successfully retrieved peers are still evaluated.

For a local InterUSS stack:

```bash
AERO_API_DURABLE_STORE=postgres
AERO_API_DATABASE_URL='postgres://aero_arc:aero_arc_dev@localhost:5432/aero_arc?sslmode=disable'
AERO_API_AIRSPACE_PROVIDERS='local,interuss'
AERO_API_DSS_BASE_URL='http://localhost:8082'
AERO_API_DSS_OAUTH_TOKEN_URL='http://localhost:8085/token'
AERO_API_DSS_OAUTH_AUDIENCE='localhost'
AERO_API_DSS_OAUTH_ISSUER='localhost'
AERO_API_DSS_OAUTH_SUBJECT='aero-arc-api'
AERO_API_DSS_ALLOW_INSECURE_PEER_URLS=true
```

Setting a USS base URL enables DSS publication and requires the PostgreSQL
durable store plus peer-request JWT verification:

```bash
AERO_API_USS_BASE_URL='https://uss.example.com'
AERO_API_USS_JWT_PUBLIC_KEY_FILE='/run/secrets/uss-auth-public.pem'
AERO_API_USS_JWT_ISSUER='trusted-oauth-issuer'
AERO_API_USS_JWT_AUDIENCE='aero-arc-api'
```

When using Compose, keep `AERO_API_USS_JWT_PUBLIC_KEY_FILE` set to the container
path shown above and set `AERO_API_USS_JWT_PUBLIC_KEY_HOST_FILE` to the public
key's host path. Compose mounts the host file read-only at the container path.

New intents receive UUIDv4 identifiers by default. When DSS publication is
enabled, caller-supplied intent IDs must also be UUIDv4 so the same identifier
is used by Aero Arc, the DSS, and peer USS endpoints.

Acceptance commits the local lifecycle change and a desired DSS state in one
transaction. A leased background reconciler performs a fresh conflict check,
creates or updates the DSS reference with the current peer OVN key, persists
the returned OVN/version/subscription, and durably retries peer notifications.
Cancellation and completion enqueue withdrawal of a published reference.
Operational synchronization state is available from
`GET /api/v1/operational-intents/{intent_id}/coordination`.

Use `AERO_API_DSS_STATIC_TOKEN` instead of the dummy OAuth settings when a
bearer token is managed externally. WGS84 polygon volumes are supported in this
slice. Malformed local geometry is rejected when written; unsupported SCD
geometry or altitude references, peer failures, and antimeridian-crossing
geometry fail closed as indeterminate findings.

Peer USS URLs require HTTPS and public network addresses by default. Enable
`AERO_API_DSS_ALLOW_INSECURE_PEER_URLS` only for a trusted local InterUSS stack.

Set `--debug` or `AERO_API_DEBUG=true` to enable debug-level operation logs.
Debug mode logs each HTTP request with method, path, status, and duration, plus
named workflow operations such as `create_intent`, `modify_intent`,
`evaluate_preflight`, `check_deconfliction`, `activate_intent`, and
`ingest_telemetry`.

## Endpoints

Health:

- `GET /healthz`
- `GET /readyz`

`/readyz` currently reports process readiness only. It does not probe registry,
telemetry, replay, or durable-store dependencies.

Dashboards:

- `GET /api/v1/overview`
- `GET /api/v1/operations`
- `GET /api/v1/preflight`
- `GET /api/v1/conformance`
- `GET /api/v1/maintenance`
- `GET /api/v1/records`

Fleet and replay:

- `GET /api/v1/aircraft`
- `POST /api/v1/aircraft`
- `GET /api/v1/aircraft/{aircraft_id}`
- `GET /api/v1/aircraft/{aircraft_id}/map?limit=500`
- `GET /api/v1/aircraft/{aircraft_id}/flights`
- `POST /api/v1/aircraft/{aircraft_id}/battery-installations`
- `GET /api/v1/flights/{flight_id}`
- `POST /api/v1/flights/{flight_id}/start`
- `GET /api/v1/flights/{flight_id}/replay?limit=500`
- `POST /api/v1/batteries`
- `POST /api/v1/maintenance-events`

Operational workflows:

- `POST /api/v1/operational-intents`
- `POST /api/v1/operational-intents/{intent_id}/modify`
- `POST /api/v1/operational-intents/{intent_id}/volumes`
- `POST /api/v1/operational-intents/{intent_id}/submit`
- `POST /api/v1/operational-intents/{intent_id}/preflight/evaluate`
- `POST /api/v1/operational-intents/{intent_id}/deconfliction/check`
- `GET /api/v1/operational-intents/{intent_id}/conflicts`
- `GET /api/v1/operational-intents/{intent_id}/coordination`
- `POST /api/v1/operational-intents/{intent_id}/accept`
- `POST /api/v1/operational-intents/{intent_id}/activate`
- `POST /api/v1/operational-intents/{intent_id}/flights`
- `GET /api/v1/operational-intents/{intent_id}/conformance`
- `POST /api/v1/telemetry`

### No-seed battery and flight bootstrap

An aircraft must have a healthy active battery installation before preflight
can clear and an intent can activate. Create the battery with `POST
/api/v1/batteries`, then install it with:

```json
POST /api/v1/aircraft/{aircraft_id}/battery-installations
{
  "id": "installation-1",
  "battery_id": "battery-1",
  "operator_id": "operator-1"
}
```

`operator_id` is optional when it can be derived from the aircraft and battery;
all nonempty operator IDs must agree. `installed_at` is optional and defaults to
server time. A second active installation for the same aircraft returns `409`
rather than silently replacing physical state.

Reserve the flight identity while the linked intent is accepted or active:

```json
POST /api/v1/operational-intents/{intent_id}/flights
{
  "id": "flight-1",
  "operator_id": "operator-1",
  "mission_type": "sitl"
}
```

The API derives `aircraft_id`, `intent_id`, `intent_version`, and the effective
operator from authoritative records and creates a `planned` flight. After the
exact linked intent version becomes active and the exact current mission has an
`applied` or `already_applied` deployment, start the flight with an empty
`POST /api/v1/flights/{flight_id}/start`. Import, deployment creation, and start
share a durable flight lifecycle fence, so start cannot race past a pending or
outcome-unknown deployment for the current mission. Starting assigns server
time to `started_at`; retrying an already-active flight is idempotent. Starting
without a verified current mission deployment, or against an accepted,
superseded, completed, or otherwise non-active intent, returns `409`.

These operator-console routes follow the API's current local, unauthenticated
single-operator deployment posture. They enforce operator consistency between
linked records, but they are not a substitute for the authentication, tenancy,
and authorization layer required before external exposure.

Authenticated USS-to-USS SCD endpoints, enabled with
`AERO_API_USS_BASE_URL`:

- `GET /uss/v1/operational_intents/{entity_id}?version={dss_version}`
- `POST /uss/v1/operational_intents`

These endpoints require a signed bearer JWT with the configured issuer and
audience plus the `utm.strategic_coordination` scope. The GET endpoint serves
only the local intent version confirmed by the DSS, never an unpublished draft
or pending replacement. Incoming peer change and deletion notifications are
validated against the token subject and durably recorded before acknowledgment.

### Current Deconfliction Behavior

The deconfliction service combines the providers named by
`AERO_API_AIRSPACE_PROVIDERS`. The local provider uses PostGIS indexes over the
authoritative operational-volume table for broad-phase geometry, time, and
altitude filtering, then loads the candidate intent version and volumes. The
evaluator:

- evaluates the target's latest version while retaining an accepted candidate
  version when a newer draft is being edited;
- compares overlapping time windows and compatible altitude references;
- accepts inline GeoJSON `Polygon` geometry;
- compares polygon bounding boxes rather than exact polygon intersections; and
- persists version-scoped conflict findings in the configured operational
  store.

InterUSS SCD discovery is optional and must be selected explicitly. When
selected, DSS references are queried and full operational-intent details are
retrieved directly from each managing USS. Unsupported or unavailable peer data
fails closed.

## Demo Data

`--seed demo` / `AERO_API_SEED=demo` populates the in-memory stores at startup
with representative aircraft, batteries, operational intents, preflight checks,
maintenance events, conformance records, evidence records, reportability
reviews, latest telemetry, live registry state, and one replay manifest.

This seed mode is intended for local UI and API testing only. It is opt-in and
supports the dashboard endpoints consumed by `aero-arc-ops`.

## Example Requests

```bash
curl http://localhost:8080/healthz
```

```bash
curl -X POST http://localhost:8080/api/v1/aircraft \
  -H 'Content-Type: application/json' \
  -d '{"id":"aircraft-1","tail_number":"N100AA","name":"Survey One","model":"AA-1"}'
```

```bash
curl http://localhost:8080/api/v1/aircraft
```

```bash
curl -X POST http://localhost:8080/api/v1/batteries \
  -H 'Content-Type: application/json' \
  -d '{"id":"battery-1","serial_number":"BAT-001","model":"AA-Pack","state_of_health":92}'
```

```bash
curl -X POST http://localhost:8080/api/v1/maintenance-events \
  -H 'Content-Type: application/json' \
  -d '{"id":"mx-1","aircraft_id":"aircraft-1","severity":"warning","title":"Inspect prop"}'
```

## Development

```bash
make build
make test
make check
```

## TODO

- Migrate the remaining in-memory durable domain groups to PostgreSQL and add a
  versioned migration runner before production deployment.
- Add real replay storage backed by S3/object storage manifests and log chunks.
- Persist and administer explicit aircraft-to-agent assignments rather than
  relying on fixture-created mappings.
- Add auth, tenancy, and authorization policy before exposing beyond local
  development.
