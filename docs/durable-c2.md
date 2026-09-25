# Durable command control

The API accepts immutable commands in PostgreSQL and dispatches them from a
background outbox worker. HTTP acceptance performs no Relay or aircraft call.
The existing Postgres-compatible durable store is the boundary for Multigres;
this implementation has been tested with PostgreSQL/PostGIS, not a Multigres
cluster or a failover exercise.

## Invariants

1. API generates one UUID per accepted submission. `(operator_id, idempotency_key)`
   is unique. Repeating the same request returns the original command; changed
   request content returns conflict. Retries never extend the deadline.
2. The canonical SHA-256 digest covers execution, authority, Agent target,
   flight/intent binding, definition version, deadline, and recovery policy.
   Relay placement and delivery attempts cannot change that digest.
3. Authorization and binding validation precede acceptance. Command, initial
   audit events, and outbox obligation commit atomically. Mission upload reserves
   the existing mission deployment record in that transaction too.
4. Only a worker with the current unexpired database lease generation may commit
   delivery evidence. Claims use database time and `SKIP LOCKED`.
5. Relay receipt and stream handoff are delivery evidence. They never establish
   Agent admission, autopilot acceptance, or resulting vehicle state.
6. Agent persists admission and consumes a SQLite first-effect permit before
   MAVLink handoff. Generic commands are never automatically re-executed after
   that permit is consumed, including across restart. An uncertain write is
   `outcome_unknown`, not permission to retry the effect.
7. Only mission upload permits the existing mission readback recovery policy.
   Current onboard digest, expiry, and binding fences remain authoritative.
8. `applied` and observation are independent. Fresh matching vehicle messages
   can confirm observation; loss of telemetry never creates confirmation.
   Later command admission supersedes pending observation attribution.
9. Evidence IDs and source timestamps are immutable. API receipt timestamps are
   separate. Contradictory terminal evidence rolls back instead of overwriting
   prior facts. Telemetry remains outside the durable command database.
10. One unresolved command blocks new commands for the aircraft. Outcome unknown
    remains unresolved even when automatic polling stops. There is no force-clear
    endpoint or automatic replacement command.

## Definitions and evolution

The API owns approved definitions. Clients select a type rather than supplying
arbitrary MAVLink parameters. Agent advertises `mavlink_command_v1` and
`mission_upload_v1`; Relay rejects unsupported execution capabilities.
The generic long/int executor accepts numeric MAVLink commands, so adding a
server definition using existing mechanisms does not require an Agent update.
New execution protocols, observation predicates, or vehicle profiles do require
an explicit capability/Agent rollout. Current vehicle profile is ArduCopter.

| Type | Execution | Observation |
| --- | --- | --- |
| ARM / DISARM | command 400, parameter 1 = 1 / 0; no force | Fresh armed / disarmed heartbeat |
| MISSION_UPLOAD | Existing mission transfer and readback workflow | Verified onboard mission digest |
| MISSION_START | command 300; exact current mission precondition | Active mission state |
| PAUSE / RESUME | command 193, parameter 1 = 0 / 1 | Unavailable; AUTO mode alone does not prove pause/resume |
| RTL | command 20 | Fresh ArduCopter RTL mode, not arrival home |
| LAND | command 21 | Fresh extended state reporting on ground |

ARM/DISARM preserve the existing ACK ambiguity and state-transition fences.
Generic commands use an ACK quiet interval and target/session checks. MAVLink
COMMAND_ACK has no application UUID: this is conservative at-most-once handoff,
not an exactly-once guarantee from the autopilot or protection from another GCS.

## Lifecycle and recovery

`accepted` is the durable database commit. `acknowledged` means Agent journal
admission. `applied` means autopilot acceptance. `observation_state` is separately
`pending`, `observed`, `unavailable`, or `superseded`. Execution rejection and
uncertain outcomes remain explicit. The initial requested/authorized/accepted
events share the commit timestamp; they are not measured HTTP phase timings.

Four process-owned workers claim 150-second leases and bound attempts to 120
seconds. Failed or incomplete attempts back off exponentially, capped at 64
seconds. Automatic evidence polling stops 15 minutes after authorization expiry;
manual reconciliation requeues the same authority without extending expiry.
Unseen expired commands reject at Agent; previously admitted commands replay
journal evidence or listen for fresh observations without another generic effect.
There is no journal garbage collection in this version.

Mission upload uses the existing deployment reconciler under the generic worker.
Its legacy combined completion result produces separate acknowledged/applied/
observed facts at the recorded result time; it cannot reconstruct earlier
per-phase timestamps. Applying MISSION_START atomically activates the flight
record, independently of later observation.

## API and Ops

All routes below use the existing mission-control bearer credential:

- `POST /api/v1/flights/{flight_id}/commands` with mandatory `Idempotency-Key` and
  `{"type":"ARM"}` (or another approved definition), returns 202 and Location.
- Upload additionally requires reviewed `mission_id` and `mission_digest`.
- `GET /api/v1/flights/{flight_id}/commands` returns complete newest-first history.
- `GET /api/v1/flights/{flight_id}/commands/{command_id}` restores one command.
- `POST /api/v1/flights/{flight_id}/commands/{command_id}/reconcile` schedules
  recovery of that exact command and returns 202.

The existing mission-deploy POST now accepts into this outbox when command
control is enabled. Its original response DTO and mission review UI are retained.
The flight-planning workflow adds flight controls, confirmation, stable-key retry,
restored history, independent observation labels, and evidence timelines.

`CommandAuthorizer` is the policy hook. Startup currently uses the existing
trusted mission-control service principal and `AERO_API_MISSION_DEPLOY_TOKEN`.
This is not per-user organization RBAC or a new identity provider. Durable storage
and existing authenticated Relay control configuration must be enabled; memory
stores cannot run command control. Read/replay access also requires the token.

## Evidence and rollout

Agent sends persisted evidence over the existing authenticated telemetry stream.
Relay routes it to the background API exchange; later exchanges recover journal
facts after lost responses. API persists immutable command events and composes
complete command history into flight replay alongside existing telemetry reads.
Command evidence is not written into the Influx `aircraft_telemetry` measurement
or the existing raw-telemetry archive. Independent command archive export is
outside this change; the command tables are the current evidence authority.

Publish the protocol module first, then deploy compatible Relay and Agent builds
before enabling the updated API/Ops controls. Existing protobuf field numbers
remain unchanged. Test on SITL before aircraft deployment. Required deployment
validation includes target vehicle behavior, reconnect during execution, API and
Agent restart, and Multigres transaction/lease behavior under failover.

## Validation record

Validated on the feature branches with the published protocol commit
`3c6531fe5040` and `GOWORK=off` for independent service tests:

- Protocol: regenerated clients, buf lint, Go tests, vet, canonical golden digest.
- API: full unit suite and vet; full PostgreSQL/PostGIS integration suite using
  an isolated PostgreSQL 14 instance. Coverage includes HTTP acceptance without
  dispatch, idempotent replay, conflicting payload, lease takeover, immutable
  evidence, contradictory terminal rejection, worker mission upload/readback,
  and mission-start activation separate from observation.
- Agent: full unit/race suites and vet; simulated LAND ACK followed by later
  touchdown, with recovery asserting one MAVLink handoff.
- Relay: full unit/race suites; capability gate, distinct receipt evidence, and
  stale-stream rejection.
- Ops: formatting, analysis, 109 widget tests, release web build. The installed
  Flutter SDK resolved newer SDK-pinned transitive packages during validation;
  the repository lockfile was retained to avoid unrelated dependency changes.

The full API integration command was attempted; InfluxDB container provisioning
failed because Docker is unavailable. Relay Docker integrations were not run.
Staticcheck v0.6.1 rebuilt with Go 1.26 reports only existing SA1019 deprecations
at `internal/registry/grpc.go:28,30` and `internal/relaycontrol/grpc_pool.go:53`.
SITL/hardware, Multigres failover, and independent command archive export remain
outside this validation record.
