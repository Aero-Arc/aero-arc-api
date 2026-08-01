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
- Spatial index: a replaceable projection of operational volumes used only for
  local conflict-candidate discovery.
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

The durable store remains authoritative and runs in `memory` mode in this
Compose slice. PostGIS is only the local spatial index. Startup rebuilds that
projection from the durable store and fails if PostGIS cannot be reached.

Configuration is available through flags or environment variables:

```bash
go run ./cmd/aero-arc-api start \
  --addr :8080 \
  --durable-store memory \
  --spatial-index memory \
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
AERO_API_SPATIAL_INDEX=memory
AERO_API_AIRSPACE_PROVIDERS=local
AERO_API_TELEMETRY_STORE=memory
AERO_API_REPLAY_STORE=memory
AERO_API_REGISTRY_MODE=memory
AERO_API_REGISTRY_ADDR=localhost:50051
AERO_API_REGISTRY_DIAL_TIMEOUT=5s
AERO_API_REQUEST_TIMEOUT=3s
AERO_API_SEED=demo
AERO_API_DEBUG=true
```

To read normalized position telemetry from InfluxDB 3 Core, set
`AERO_API_TELEMETRY_STORE=influxdb` together with
`AERO_API_INFLUXDB_HOST`, `AERO_API_INFLUXDB_TOKEN`, and
`AERO_API_INFLUXDB_DATABASE`. Demo seeding remains available only with the
memory telemetry store.

`AERO_API_REGISTRY_MODE=grpc` connects to the real `aero-arc-registry` gRPC
service. Durable and replay stores still run in `memory` mode in this scaffold.

## Deconfliction Read/Check Slice

Set `AERO_API_SPATIAL_INDEX=postgis` and `AERO_API_POSTGIS_DATABASE_URL` to use
PostGIS for local spatial candidate discovery. The required spatial schema is
initialized automatically. Operational intents, versions, volumes, and
conflict findings remain authoritative in `AERO_API_DURABLE_STORE`.

Airspace sources are explicit and composable. `local` queries the configured
spatial index and hydrates candidates from the durable store. `interuss` queries
DSS references for each submitted WGS84 volume and fetches full details from
each managing USS. Peer failures produce an indeterminate, blocking finding
while successfully retrieved peers are still evaluated.

For a local InterUSS stack:

```bash
AERO_API_SPATIAL_INDEX=postgis
AERO_API_POSTGIS_DATABASE_URL='postgres://aero_arc:aero_arc_dev@localhost:5432/aero_arc?sslmode=disable'
AERO_API_AIRSPACE_PROVIDERS='local,interuss'
AERO_API_DSS_BASE_URL='http://localhost:8082'
AERO_API_DSS_OAUTH_TOKEN_URL='http://localhost:8085/token'
AERO_API_DSS_OAUTH_AUDIENCE='localhost'
AERO_API_DSS_OAUTH_ISSUER='localhost'
AERO_API_DSS_OAUTH_SUBJECT='aero-arc-api'
AERO_API_DSS_ALLOW_INSECURE_PEER_URLS=true
```

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
- `GET /api/v1/flights/{flight_id}`
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
- `POST /api/v1/operational-intents/{intent_id}/accept`
- `POST /api/v1/operational-intents/{intent_id}/activate`
- `GET /api/v1/operational-intents/{intent_id}/conformance`
- `POST /api/v1/telemetry`

### Current Deconfliction Behavior

The deconfliction service combines the providers named by
`AERO_API_AIRSPACE_PROVIDERS`. The local provider uses the selected spatial
index for broad-phase geometry, time, and altitude filtering, then reloads the
candidate intent version and volumes from the authoritative durable store. The
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

- Add a persistent durable-store adapter and an outbox-backed spatial projector;
  the current vertical slice updates the projection synchronously and fails
  local checks closed after a projection error.
- Harden the InfluxDB telemetry schema and production query integration.
- Add real replay storage backed by S3/object storage manifests and log chunks.
- Expand registry mapping once aircraft-to-agent identity is finalized. For now,
  `aircraft.agent_id` maps durable aircraft records to registry agents, falling
  back to `aircraft.id`.
- Add auth, tenancy, and authorization policy before exposing beyond local
  development.
