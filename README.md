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
  flight records, operational intents, and conformance events.
- Telemetry store: queryable time-series telemetry samples.
- Replay store: raw replay manifests and log chunk locations.

Raw high-frequency telemetry ingestion belongs on the relay sink path, not
through this API service.

## Run Locally

The scaffold defaults to in-memory stores and an in-memory registry client.

```bash
go run ./cmd/aero-arc-api start
```

Configuration is available through flags or environment variables:

```bash
go run ./cmd/aero-arc-api start \
  --addr :8080 \
  --durable-store memory \
  --telemetry-store memory \
  --replay-store memory \
  --registry-mode memory
```

```bash
AERO_API_ADDR=:8080
AERO_API_DURABLE_STORE=memory
AERO_API_TELEMETRY_STORE=memory
AERO_API_REPLAY_STORE=memory
AERO_API_REGISTRY_MODE=memory
AERO_API_REGISTRY_ADDR=localhost:50051
```

`AERO_API_REGISTRY_MODE=grpc` connects to the real `aero-arc-registry` gRPC
service. Durable, telemetry, and replay stores still run in `memory` mode in
this scaffold.

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /api/v1/aircraft`
- `POST /api/v1/aircraft`
- `GET /api/v1/aircraft/{aircraft_id}`
- `GET /api/v1/aircraft/{aircraft_id}/flights`
- `GET /api/v1/flights/{flight_id}`
- `GET /api/v1/flights/{flight_id}/replay?limit=500`
- `POST /api/v1/batteries`
- `POST /api/v1/maintenance-events`

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

- Add real durable store implementations for TiDB/Postgres.
- Add real telemetry store implementations, such as InfluxDB or another
  queryable time-series backend.
- Add real replay storage backed by S3/object storage manifests and log chunks.
- Expand registry mapping once aircraft-to-agent identity is finalized. For now,
  `aircraft.agent_id` maps durable aircraft records to registry agents, falling
  back to `aircraft.id`.
- Add auth, tenancy, and authorization policy before exposing beyond local
  development.
