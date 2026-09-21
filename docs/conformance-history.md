# Durable conformance history

The API does not query Conformance's PostgreSQL tables. Live summaries remain
Registry projections; immutable incident transitions are read from Conformance
using the additive `ListConformanceEvents` gRPC contract.

## Configuration

Set all five values together; there is no plaintext fallback:

```
AERO_API_CONFORMANCE_ADDR=conformance:50052
AERO_API_CONFORMANCE_CA_FILE=/certs/ca.crt
AERO_API_CONFORMANCE_CERT_FILE=/certs/api.crt
AERO_API_CONFORMANCE_KEY_FILE=/certs/api.key
AERO_API_CONFORMANCE_SERVER_NAME=conformance
```

The certificate identity must be trusted by the Conformance server. Reads inherit
the HTTP request deadline. Deploy the updated Conformance service before enabling
this API configuration. Disabled, unreachable, and pre-history-RPC deployments
return HTTP 503 for history without disabling live dashboard reads.

## HTTP contract

`GET /api/v1/operational-intents/{intent_id}/conformance/events`

The API resolves the durable intent first (404 if absent), then uses its ID as
the assignment scope. Parameters:

- `generation`: optional exact assignment generation; omitted/0 includes all.
- `from`, `until`: optional RFC3339 event-time bounds, inclusive/exclusive.
- `page_size`: 1–200; defaults to 50.
- `page_token`: opaque continuation; reuse the same scope and time filters.

Success returns `events` (always an array) and optional `next_page_token`.
Rows are newest first by `(observed_at, event_id)`, including open and resolved
transitions. Pagination is not a snapshot: concurrent late inserts require a
first-page refresh. A malformed query/token returns 400. A successful empty array
means no recorded events in that scope, **not** that the live aircraft conforms.

Each event carries `id`, `assignment_id`, `assignment_generation`, `intent_id`,
`intent_version`, `aircraft_id`, `flight_id`, `incident_id`, `transition`,
`violation_type`, `observed_at`, `frame_id`, and `evaluation_revision`.
`deviation_m` is present only for spatial evidence; measured zero is retained.
Temporal events do not currently record deviation seconds: the API does not
invent them from elapsed wall time or a zero-valued meter field.

This read endpoint follows the API's existing deployment authentication boundary;
it does not introduce operator authorization or grant browser database access.
Legacy `/conformance.events` and replay/map event lists remain legacy API evidence;
this endpoint is the continuous Conformance worker's authoritative history path.

## Validation

HTTP-to-gRPC tests use bufconn and the existing in-memory durable intent store.
No external database is needed at this boundary. Actual PostgreSQL pagination,
generation isolation, and index migration are tested in Conformance's repository.
