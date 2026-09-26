# Evidence-driven flight finalization

Mission import accepts optional `ending_behavior: rtl | land`. Ops defaults to
RTL. The choice becomes a terminal onboard mission item and part of the canonical
digest; changing the choice under an existing import key conflicts. Omitting the
field preserves existing WPL import behavior. RTL follows the autopilot's HOME
and RTL settings; explicit waypoint coverage does not prove the return corridor.

Agent tracks an applied mission start, observed airborne state, terminal mission
progress or early RTL/LAND, then fresh landed and disarmed observations. These
are independent milestones. The aircraft executes recovery from its onboard
mission without an HTTP request or internet connection. A missing observation
leaves completion pending; it never fabricates a successful flight.

The Agent journal persists milestones and a deterministic completion event. Relay
advertises durable admission only when `completion_outbox_path` is configured,
commits the exact event to SQLite before issuing its digest receipt, and retains
an API delivery obligation. Disk durability is local, not replicated Relay HA.
The API polls registered Relays, durably admits exact flight/mission/Agent-bound
evidence, then acknowledges Relay delivery. A lost receipt causes safe replay.

The API worker resolves outstanding commands, closes the exact Conformance
assignment, and clears Agent operation context. A database-clock lease fences
its final transaction: flight complete, intent complete, optional DSS withdrawal
request, and one `flight_finalized_outbox` row commit together. Closure errors
remain retryable. Physical completion time and the monitoring authority boundary
are separate; already committed Conformance history is preserved.

`GET /api/v1/flights/{flight_id}/completion` returns evidence and finalization
progress under the existing command-control authentication. A 404 means no
completion event has been admitted. Ops displays finalization separately from
command ACK/application and disables new controls after evidence admission.
`POST /api/v1/operational-intents/{intent_id}/complete` now requires command
credentials and recorded aircraft completion evidence; it reports durable progress
with 202 rather than forcing a lifecycle transition without aircraft evidence.

## Archive boundary

The finalized outbox is the producer boundary for `aero-arc-archive-worker`.
The worker currently has a draft immutable-fragment/manifest implementation.
Snapshot capture, full telemetry pagination and late-evidence watermarks, outbox
job delivery, archive discovery, and replay UI integration remain outstanding.
An outbox row is not an uploaded archive and monitoring closure is not a claim
that all delayed evidence has arrived.
