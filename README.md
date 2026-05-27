# aero-arc-api

`aero-arc-api` is the control-plane HTTP facade for Aero Arc UIs.

It connects to `aero-arc-registry` over gRPC and exposes JSON endpoints that
frontends can consume directly.

## Endpoints

- `GET /healthz`
- `GET /v1/overview`
- `GET /v1/relays`
- `GET /v1/agents`
- `GET /v1/placements`

## Configuration

Environment variables:

- `AERO_API_HTTP_ADDR` (default `:8081`)
- `AERO_API_REGISTRY_ADDR` (default `localhost:50052`)
- `AERO_API_REGISTRY_DIAL_TIMEOUT` (default `5s`)
- `AERO_API_REQUEST_TIMEOUT` (default `3s`)

Duration values can be passed as Go durations (`5s`, `500ms`) or as integer
milliseconds.

## Run

```bash
go run ./cmd/aero-arc-api
```

Example:

```bash
AERO_API_REGISTRY_ADDR=localhost:50052 go run ./cmd/aero-arc-api
```
