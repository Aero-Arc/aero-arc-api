# Intent-bound mission import

The first mission slice imports a Mission Planner/QGroundControl `QGC WPL 110`
file into an immutable API record. A mission describes what the aircraft will
be commanded to do. It is deliberately separate from the operational intent,
which remains the authority describing where and when the flight may operate.
Import never creates, replaces, or modifies operational volumes.

## HTTP contract

Import a new flight-local mission version:

```http
POST /api/v1/flights/{flight_id}/missions/import
Content-Type: application/json
Authorization: Bearer <mission-control-token>
Idempotency-Key: mission-import-2026-08-26-001

{
  "source_format": "qgc_wpl_110",
  "aircraft_id": "aircraft-sitl-1",
  "intent_id": "33333333-3333-4333-8333-333333333333",
  "intent_version": 1,
  "source": "QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t-35.363262\t149.165237\t0\t1\n1\t0\t0\t22\t0\t0\t0\t0\t-35.363262\t149.165237\t20\t1\n..."
}
```

The aircraft and intent fields are stale-screen preconditions, not client-owned
bindings. The API loads the flight and derives its aircraft and exact intent
version. A mismatch is rejected. Imports are only accepted while the flight is
`planned` and its exact intent version is the current `accepted` or `active`
version.

A new import returns `201`. An exact retry with the same idempotency key returns
the original immutable mission and `200` with `Idempotent-Replayed: true`.
Reusing a key with any different request returns `409`.

Read the newest version for a flight:

```http
GET /api/v1/flights/{flight_id}/missions/current
```

Deploy that exact current version without accepting mission bytes or topology
from the browser:

```http
POST /api/v1/flights/{flight_id}/missions/{mission_id}/deploy
Authorization: Bearer <mission-control-token>
Idempotency-Key: mission-deploy-2026-08-26-001
If-Match: "<lowercase mission_digest>"
Content-Length: 0
```

The mission identity in the path and quoted digest in `If-Match` are mandatory
review-to-deploy preconditions. If either no longer identifies the exact current
immutable mission, the API returns `409` and dispatches nothing. The browser
still supplies no plan or control-plane routing.

The API re-reads the planned flight, current mission, current accepted/active
intent version, aircraft, operator, and `aircraft.agent_id` immediately before
dispatch. It asks Registry for the Agent's authoritative placement, maps the
returned Relay ID through Registry's Relay list, and opens an authenticated
mTLS control connection to that discovered address. The request never accepts
an Agent ID, Relay ID/address, session ID, or raw mission from the caller.

Before `DeployMission`, the API sends a stable, idempotent
`SetOperationContext` for the exact aircraft, flight, intent ID, and intent
version. A missing or rejected Agent acknowledgement prevents mission dispatch.
Both the context-command ID and deploy-command ID remain stable across API,
Registry-placement refresh, Relay, and Agent retries.

The synchronous response and durable status use `pending`, `applied`,
`already_applied`, `rejected`, `temporary_error`, `outcome_unknown`,
`binding_mismatch`, and `onboard_mission_mismatch`. Transport deadlines and
ambiguous responses are retained as `outcome_unknown`; reconciliation reuses
the same command ID, binding, plan, issued time, and expiry. Exact terminal
retries return the original record with `Idempotent-Replayed: true`; reusing an
idempotency key after the current mission changes returns `409`.

`expires_at` fences the Agent's first aircraft effect. It does not prevent
readback recovery for a command the Agent had already durably marked
`effect_started` or `outcome_unknown`. The API may resend that exact immutable
command through `reconcile_until`; the Agent rejects a post-expiry first effect
but can finish readback reconciliation for an existing uncertain WAL record.
At `reconcile_until` the API stops redispatching and retains
`outcome_unknown` for explicit operational resolution. That unresolved record
continues to fence replacement missions, intent/volume mutations, another
aircraft deployment, and flight start indefinitely; expiry never silently
authorizes a conflicting effect. Clearing it requires an authoritative
correlated outcome or an explicit future manual-resolution workflow.

Read one deployment result, scoped to its flight:

```http
GET /api/v1/flights/{flight_id}/mission-deployments/{deployment_id}
Authorization: Bearer <mission-control-token>
```

After a UI reload, restore the authoritative deployment for the flight's
current mission without retaining the original idempotency key in the browser:

```http
GET /api/v1/flights/{flight_id}/mission-deployments/current
Authorization: Bearer <mission-control-token>
```

The read prefers an outstanding `pending`, `temporary_error`, or
`outcome_unknown` command over terminal history for that exact current mission.
Creation is serialized on the flight and rejects a different client idempotency
key while that command is outstanding, so a reload or concurrent click cannot
create a second unresolved aircraft effect. Reloaded clients use the durable
deployment ID and reconciliation route instead of inventing a replacement key.
Retry that durable command with an empty request:

```http
POST /api/v1/flights/{flight_id}/mission-deployments/{deployment_id}/reconcile
Authorization: Bearer <mission-control-token>
Content-Length: 0
```

The retry accepts no idempotency key, mission bytes, binding, command ID, Agent,
Relay, or address. The API loads all of them from the scoped durable deployment,
revalidates the current flight/mission/intent/aircraft binding, refreshes
Registry placement, and reuses the original command IDs. A terminal deployment
is returned without redispatch. A deployment ID from another flight returns
`404`.

Configure the outbound control plane as one all-or-none mTLS identity:

```bash
AERO_API_REGISTRY_MODE=grpc
AERO_API_RELAY_CONTROL_CA_FILE=/run/secrets/relay-ca.pem
AERO_API_RELAY_CONTROL_CERT_FILE=/run/secrets/api-relay-client.pem
AERO_API_RELAY_CONTROL_KEY_FILE=/run/secrets/api-relay-client-key.pem
AERO_API_RELAY_CONTROL_SERVER_NAME=relay.internal
AERO_API_MISSION_DEPLOY_TOKEN='<at-least-24-random-bytes>'
AERO_API_RELAY_CONTROL_TIMEOUT=45s
AERO_API_RELAY_PLACEMENT_TTL=10s
```

Partial TLS configuration, a short/missing control token, or Relay control with
the in-memory Registry is rejected at startup. Without the complete control
configuration, import and deployment fail closed with `503`; current-mission
reads remain available. Import, deployment, and deployment-status reads require
the same dedicated bearer token. This is a bounded first-slice control guard
because the API does not yet have operator identity/session authorization. Do
not compile it into a public web bundle; inject it only into a trusted/local Ops
session.

`GET /api/v1/aircraft/{aircraft_id}/map` includes that route as
`commanded_mission` only when it is the current mission of the exact active
flight, its aircraft and intent-version binding match the active intent, and an
`applied` or `already_applied` deployment verifies the same mission digest. A
new import for another planned flight cannot replace the active route. The
commanded route remains visually and structurally distinct from
`operational_volumes` (authorized area) and `replay_samples` (observed track).

## Validation and canonical form

The JSON body is bounded to 2 MiB and the WPL source to 1 MiB, 4 KiB per line,
and 200 mission items. Unknown JSON fields, malformed numeric values, non-finite
values, non-contiguous sequences, and values outside coordinate/altitude ranges
are rejected with machine-readable `validation_findings`.

ArduPilot/Mission Planner WPL row `0` is HOME/export metadata, not an onboard
operational mission item. Import requires that source row to be sequence `0`,
`current=1`, frame `0`, and command `16`. The API validates it but excludes it
from `Mission.items`, volume coverage, and `mission_digest`. At least one
operational source row must follow it. Source rows `1..N` must be contiguous and
set `current=0`; they become canonical mission sequences `0..N-1`, all with
`current=false`. `source_sha256` still covers the exact complete WPL text,
including HOME metadata.

This slice intentionally accepts only:

- WPL frame `0` (`MAV_FRAME_GLOBAL`, MSL altitude)
- `MAV_CMD_NAV_WAYPOINT` (`16`)
- `MAV_CMD_NAV_LAND` (`21`)
- `MAV_CMD_NAV_TAKEOFF` (`22`)
- `autocontinue=1` on every operational item
- zero values for operational parameters 1 through 4, except LAND source
  parameter 4 may be `0` or `1`; canonical parameters 1 through 3 and
  waypoint/takeoff parameter 4 are positive zero, while LAND parameter 4 is `+1`
- operational altitudes that remain bit-identical after ArduPilot's exact
  float32 multiply, int32 truncation-toward-zero, and float32 centimeter
  readback path

Relative and terrain frames are rejected because the API cannot safely resolve
them into the operational volume's altitude reference. The exact intent version
must contain exactly one MSL Polygon volume covering the full intent window.
Multiple volumes are rejected because WPL 110 does not provide a waypoint time
schedule with which to choose among them. Every operational item altitude,
point, and entire consecutive operational route segment must be covered by that
volume. HOME metadata is not checked as commanded route. PostgreSQL deployments
use `ST_Covers`; the memory store implements the same deterministic point and
segment checks, including concave polygons and holes.

Coordinates are canonicalized to signed degrees times `1e7`; accepted parameters
and altitude are canonicalized through IEEE-754 float32. ArduPilot stores and
reads back NAV_LAND command `21` parameter 4 as `+1` when WPL supplied either
`0` or `1`, so the API canonical mission and digest always contain `+1` for that
field. Other values that ArduPilot would normalize—including nonzero parameters,
disabled autocontinue, sub-centimeter altitude, or values such as `16.8 m` that
float32 multiplication truncates to `1679 cm` and reads back as `16.79 m`—are
rejected before persistence so an Agent readback cannot false-mismatch the API
digest.
`source_sha256` still hashes the exact uploaded source. `mission_digest` is
SHA-256 over the published schema-version `1` fixed-width canonical byte
encoding. The encoder uses an explicit domain prefix, item count, network-byte
order integers, IEEE-754 bit patterns, and one-byte booleans, so the digest is
stable across protobuf implementations and runtimes.

The API does not require E7 coordinates to survive legacy MAVLink float32
latitude/longitude conversion. `MISSION_ITEM_INT` preserves the canonical E7
contract; link capability negotiation and fail-closed legacy fallback remain an
Agent concern rather than a reason to reject otherwise valid intent-contained
routes globally.

## Lifecycle and persistence fence

Aircraft, flight, mission, mission-item, and deployment records used by this
slice are stored in PostgreSQL. Foreign keys prevent missions and deployments
from referring to nonexistent aircraft or flights. Exact import and deployment
idempotency replays remain available after an API restart and after the flight
has started; conflicting key reuse is still rejected. A terminal deployment
replay is returned only while its exact immutable mission remains current for
the flight, so an old successful result cannot be mistaken for a replacement
mission's deployment.

Mission import, deployment creation, binding mutations, and flight start share
durable flight, aircraft, and intent fences in a single lock order. Deployment
creation rechecks the exact current planned flight, immutable mission, and
accepted-or-active intent before committing a retryable deployment record. That
record is the dispatch authorization fence: while it remains `pending`,
`temporary_error`, or `outcome_unknown`, replacement import, intent or volume
mutation, conflicting deployment, and start fail with `409`. Therefore a
binding mutation either commits before deployment creation and makes its final
recheck fail, or waits behind the durable deployment fence until a terminal
outcome; it cannot invalidate an authorized command between validation and the
aircraft effect.

The same aircraft-level fence prevents a planned flight from uploading over an
active flight. A database-assigned monotonic creation sequence, rather than
application wall-clock timestamps, defines the authoritative order across API
replicas and delayed requests. Start rejects any redispatchable outstanding
fence and requires the aircraft's latest deployment—not any historical
success—to belong to the starting flight's exact current mission and report `applied` or
`already_applied` with the matching digest. A newer rejection, binding/readback
mismatch, or deployment for another flight invalidates an older success. The
store also permits at most one active flight per aircraft. Terminal history for
an older mission version does not poison a newer, independently verified
current mission.

## Deliberate deferrals

This slice deploys and records Agent/autopilot acknowledgement and exact onboard
digest readback, but it does not activate the operational intent, complete the
flight, switch vehicle mode, arm, or begin mission execution. Flight start is a
separate API lifecycle call and now requires the exact current mission to have a
verified deployment. The API also does not infer operational volumes from
waypoints. Those remain explicit lifecycle/C2 operations rather than side
effects of uploading a mission.

Multipart upload is deferred. JSON source text keeps the first UI/API slice
small while retaining strict size limits, unknown-field rejection, durable
idempotency, and an unambiguous source hash.
