# Deconfliction PostGIS: Production Follow-up

## Purpose and current boundary

The deconfliction vertical slice uses PostgreSQL/PostGIS as the shared local
working set for an Aero Arc cluster. API replicas persist operational intents,
volumes, and conflict findings in the same database that performs spatial
candidate filtering. The DSS remains the network-wide coordination authority.

The local query is deliberately asymmetric:

- A local candidate can establish a possible conflict immediately.
- No local candidate is only a preliminary clear result until the DSS has been
  queried successfully with an acceptably fresh view.

The in-memory durable store is a test implementation. The current PostgreSQL
store still embeds it for durable domain groups outside this vertical slice so
the existing application-wide `durable.Store` can be wired. That compatibility
fallback is not a production architecture and must be removed before enabling
PostgreSQL mode in production.

## Guarantees completed in this slice

- PostgreSQL is authoritative for operational intents, intent revisions,
  volumes, findings, and the PostGIS candidate index.
- Intent creation rejects duplicate `(id, version)` records.
- An internal monotonic revision protects same-version updates from lost
  updates without changing the domain meaning of operational-intent version.
- Intent-scoped PostgreSQL advisory locks serialize volume and finding writes
  and set replacement across API replicas.
- Replacement calls reject children outside the requested intent, version, or
  rule scope before deleting existing records.
- Volumes and conflict findings reference an existing operational-intent
  version.
- Intent and volume windows, altitude bounds, geometry validity, and finite
  buffers are constrained in the database.

## Required before production

### Remove the memory fallback

Continue splitting application dependencies into capability-focused store
interfaces. `durable.OperationalStore` is the boundary for this slice. Add
PostgreSQL implementations and narrow interfaces for the remaining domain
groups, then remove `*memory.Store` from `postgres.Store`.

Production startup must fail when a required durable capability is not backed
by a configured production store. It must never silently create process-local
state. Keep the memory implementation available to unit tests and explicitly
selected development configurations only.

### Introduce versioned migrations

The embedded `schema.sql` is bootstrap tooling for this vertical slice.
`CREATE ... IF NOT EXISTS` cannot evolve existing constraints and columns.
Before the first production deployment:

1. Select a migration runner and create an initial versioned migration from the
   current schema.
2. Run migrations as a deployment step with a suitably privileged role.
3. Run the API with a less-privileged role that cannot install extensions or
   alter schemas.
4. Add migration upgrade and rollback/forward-repair tests using a database
   populated with the previous schema version.

### Close the add-volume/lifecycle race

Intent updates and volume writes now share an advisory-lock namespace, but the
service currently reads lifecycle state before beginning the store operation.
A volume request that read `draft` can wait behind a transition and then write
after that transition commits.

Move “verify the current revision/status and write the volume” into one store
transaction, or introduce a command method that atomically performs that
operation. Add the equivalent invariant anywhere a business decision and its
write currently span separate store calls.

## DSS-backed local working set

Do not cache only operations that conflict at the instant they are discovered.
Persist relevant nearby operations returned through DSS/peer discovery so a
later time, altitude, buffer, or geometry change can be evaluated locally.

Cached external operations need explicit metadata:

- source provider and owning USS/manager;
- DSS entity identifier and OVN/version;
- time fetched and time last confirmed;
- expiration or freshness deadline;
- synchronization state and last error;
- whether the record is locally owned or externally discovered.

Choose and document whether external operations share the operational intent
tables or use dedicated cache tables. Shared tables simplify one PostGIS query,
but require strong source/ownership constraints so a remote refresh cannot
overwrite locally authored workflow state. Dedicated tables make ownership and
eviction safer but require a union in candidate discovery.

## Submission and synchronization workflow

Avoid a request path that commits PostgreSQL and then makes an untracked DSS
network call. Use a transactional outbox:

1. Commit the local intent change and an outbox event together.
2. Have a worker submit the event to the DSS with an idempotency identity.
3. Persist the DSS OVN and acknowledgement on success.
4. Retry transient failures with bounded exponential backoff and jitter.
5. Move permanent failures to an inspectable terminal state.
6. Reconcile ambiguous timeouts by reading DSS state before resubmitting.

Suggested externally visible result states are `local_preliminary`,
`dss_pending`, `dss_confirmed`, and `indeterminate`. A stale or unavailable DSS
must never be represented as a globally authoritative clear result.

## Query and index validation

Create production-shaped datasets and record `EXPLAIN (ANALYZE, BUFFERS)` for
candidate queries. Validate at least:

- dense urban geometry and many target volumes;
- sparse regional geometry;
- rows with unknown footprints;
- large and highly variable buffers;
- time windows containing many expired operations;
- warm- and cold-cache behavior.

The exact `ST_DWithin` radius currently depends on both rows' buffer values.
Verify that the GiST index meaningfully restricts candidates. If it does not,
add an indexable constant upper-bound or precomputed envelope before the exact
variable-radius predicate. Consider a range index for time-window overlap once
measurements justify it.

## Required test expansion

- Run PostgreSQL integration tests in CI against the supported PostgreSQL and
  PostGIS versions, not only against the memory implementation.
- Exercise two independent connection pools to model separate API replicas.
- Add repeated/high-contention tests for intent transitions, volume writes,
  finding replacement, and DSS cache refresh.
- Add failure-injection tests at every DSS/outbox boundary: timeout before
  acknowledgement, timeout after acknowledgement, duplicate delivery, stale
  OVN, peer failure, and partial provider availability.
- Add a multi-instance end-to-end test proving that a record written through
  one API replica is immediately queryable through another.
- Add load tests with latency percentiles and query-plan regression capture.

## Observability and operations

Track local candidate latency, candidates examined, cache age, DSS latency,
DSS error class, outbox depth and age, retries, revision conflicts, advisory
lock wait time, and indeterminate outcomes. Alerts should focus on stale cache
coverage, growing outbox age, and sustained DSS failure rather than treating a
local database hit rate as proof of global correctness.

## Production acceptance criteria

The design is ready for production only when:

- no production durable capability falls back to process memory;
- migrations can upgrade a populated prior schema;
- all state-changing operations have documented concurrency semantics;
- local versus DSS-confirmed results are distinguishable;
- DSS submission is recoverable and idempotent;
- cache freshness and ownership are enforced;
- multi-replica, failure-injection, and representative-load tests pass; and
- operational dashboards expose synchronization health and stale data.
