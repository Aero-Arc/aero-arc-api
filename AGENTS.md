# Aero Arc API contributor context

## Architecture

- `internal/domain` contains stable API/domain types. JSON is snake_case.
- `internal/service` composes durable records, telemetry, registry state, and
  read models. Frontends call this API; they do not call backing services.
- `internal/store/durable` owns authoritative business records.
- `internal/store/telemetry` owns time-series reads. InfluxDB data is written by
  `aero-arc-relay`, not by this API. The relay contract is the
  `aircraft_telemetry` measurement documented in `docs/ui-data-contract.md`.
- `internal/registry` is a gRPC client boundary. Durable `Aircraft.agent_id` is
  the only valid aircraft-to-agent mapping; never infer it from aircraft ID.
- Deconfliction and DSS publication are documented separately in
  `docs/deconfliction-publication.md`.

## Live aircraft state

- `GET /api/v1/aircraft/{aircraft_id}/state` is the focused composite endpoint.
- `GET /api/v1/operations` includes the same records in `live_aircraft`.
- Telemetry message groups are independent observations. Do not merge their
  timestamps or assume a battery record was sampled with a position record.
- Registry liveness and telemetry freshness are separate. Defaults are 30s and
  15s, configured by `AERO_API_REGISTRY_FRESHNESS` and
  `AERO_API_TELEMETRY_FRESHNESS`.
- Latest-state InfluxDB ranking is limited to five minutes by default through
  `AERO_API_TELEMETRY_LATEST_LOOKBACK`. Keep it at least as large as telemetry
  freshness. This bound does not apply to replay/history queries.
- Preserve missing versus numeric zero when decoding sparse InfluxDB columns.

## Testing

Use writable caches in restricted environments:

```bash
GOCACHE=/tmp/aero-arc-api-gocache \
CCACHE_DIR=/tmp/aero-arc-api-ccache \
TMPDIR=/tmp/aero-arc-api-tmp \
go test ./...
```

Integration tests use the `integration` build tag. With no test-service
environment variables, package-scoped Testcontainers fixtures start the pinned
PostGIS and InfluxDB images, allocate dynamic ports, provision databases, and
clean up after `TestMain`; Docker must be available. Set
`AERO_API_TEST_POSTGIS_URL` to use an externally managed PostGIS instance. Set
`AERO_API_TEST_INFLUXDB_HOST`, `AERO_API_TEST_INFLUXDB_TOKEN`, and
`AERO_API_TEST_INFLUXDB_DATABASE` together to use an externally managed
InfluxDB database. Do not make bufconn/httptest integrations depend on Docker,
and keep DSS integration tests explicitly configured against an external DSS.

Before handing off changes, run `gofmt`, `go test ./...`,
`go test -tags=integration ./...`, `go vet ./...`, and `staticcheck ./...` when
the tool is installed.
