# DSS Operational-Intent Publication

## Purpose

This document describes how Aero Arc publishes locally owned operational
intents to an InterUSS Discovery and Synchronization Service (DSS), serves the
corresponding USS details, and notifies subscribed peer USSs.

The central constraint is that PostgreSQL and the DSS cannot participate in one
atomic transaction. Aero Arc therefore records the intended external state
durably, reconciles it with the DSS, and records the confirmed external state
separately. A local lifecycle transition is not treated as proof that the DSS
has accepted the same transition.

This is the implementation contract for the current vertical slice. The public
API and environment configuration remain documented in the repository
[README](../README.md).

## Ownership boundaries

| Component | Owns |
| --- | --- |
| Intent service | Local lifecycle rules, version changes, preflight readiness, and activation orchestration |
| Deconfliction service | Conflict evaluation, DSS reconciliation, peer notification construction, and delivery |
| PostgreSQL durable store | Local intent versions and volumes, publication work, leases, revisions, and notification outboxes |
| DSS | The network-visible operational-intent reference, OVN, state, and subscriptions |
| Local USS endpoints | DSS-confirmed operational-intent details and authenticated inbound peer notifications |

The DSS stores a reference, not the complete operational intent. Peers discover
the reference through the DSS and retrieve the full details from the managing
USS URL advertised by Aero Arc.

## Durable records

### Operational intent

An operational intent is the authoritative local workflow record. Its `status`
describes the local lifecycle, such as `draft`, `accepted`, `active`,
`complete`, or `canceled`. Its `version` describes the business version of the
flight plan. The internal `revision` is a compare-and-swap token used to reject
lost updates to the same version.

When publication is enabled, intent identifiers must be UUIDv4 values. The same
identifier is used by Aero Arc, the DSS, and the USS details endpoint; no
string-to-SCD identifier translation is required in the workflow.

### Operational-intent publication

There is one coalescing publication record per intent ID. It stores:

- the desired intent version and desired DSS state;
- the version and state last confirmed by the DSS;
- the current DSS version, OVN, subscription ID, manager, and USS URL;
- the synchronization status, attempt count, retry time, lease, and last error;
- the raw reference needed to recover or inspect coordination state.

The distinction between desired and confirmed state is deliberate. For
example, `desired_state=Activated` with `confirmed_state=Accepted` means that
activation still needs to be reconciled; it does not mean the operation is
active in the DSS.

Publication synchronization states are:

| Status | Meaning |
| --- | --- |
| `pending` | Durable work is ready to be claimed |
| `processing` | A worker holds a time-bounded lease |
| `retrying` | A transient or ambiguous outcome will be retried after backoff |
| `confirmed` | The desired version and non-terminal DSS state were confirmed |
| `blocked` | A permanent input or DSS rejection requires a new local request or operator action |
| `withdrawn` | The DSS reference is confirmed absent and no version is currently published |

### Peer notifications

Outbound notifications are a second durable outbox. Publication confirmation
and notification insertion commit in the same PostgreSQL transaction. A
notification is leased, delivered, and marked complete separately, with
exponential backoff after transient failures.

Notification IDs are deterministic, so rebuilding the same notification does
not create an additional outbox row. Delivery is at least once: if a peer
accepts a request but its response is lost, Aero Arc may send the notification
again. Peer USS receivers must therefore handle duplicate notifications
idempotently.

Inbound peer notifications are authenticated and durably recorded before Aero
Arc acknowledges them.

## Lifecycle behavior

### Create and edit

Creating or editing a draft changes only local state. A caller-supplied ID is
validated as UUIDv4 when DSS publication is enabled. Operational volumes remain
editable only while the current intent is a draft.

Modifying an accepted version creates a newer draft version. The prior accepted
version remains the locally selected and DSS-published reservation until the
new version is accepted, canceled, or otherwise reaches a terminal outcome.
This prevents an in-progress edit from silently removing the existing
reservation.

### Accept

Before acceptance, the deconfliction publisher validates that the current
version and volumes can be represented by the supported SCD model. PostgreSQL
then commits these two changes atomically:

1. the local version becomes `accepted`; and
2. its desired DSS state becomes `Accepted` with synchronization `pending`.

The background worker performs a fresh deconfliction check before mutating the
DSS. A clear result permits create or update. A potential conflict blocks the
publication. A dependency-driven indeterminate result, such as a temporary DSS
or peer-details failure, remains retryable and fails closed.

Accepting a newer version supersedes older accepted versions in the same
transaction. Candidate selection also ignores an accepted version when a newer
accepted or active version exists, so stale footprints cannot continue to
reserve airspace.

### Activate

Activation is intentionally synchronous because local `active` status is the
takeoff authority boundary. The intent service requires:

1. the latest deconfliction result to be clear;
2. current operational volumes;
3. fresh, non-blocking preflight checks;
4. no blocking compliance findings;
5. the same intent version to be DSS-confirmed as `Accepted`; and
6. that version to be DSS-confirmed as `Activated` before committing local
   `active` status.

If DSS activation succeeds but the local status update loses a revision race,
the service durably restores the correct desired external state. It restores
`Accepted` when an accepted reservation still owns the operation and requests
withdrawal when the concurrent local transition made the operation terminal.
Immediate reconciliation is best effort; the durable worker completes the
compensation after request cancellation or process interruption.

If a prior request activated the DSS but failed before the local write, a later
activation request can recognize the already-confirmed DSS state and finish
the local transition.

### Complete and cancel

Completion and cancellation atomically commit the terminal local status and a
desired DSS withdrawal. The worker deletes the DSS reference using the last
known OVN and notifies affected peers. Terminal transitions also retire any
older accepted version so an obsolete local reservation cannot be selected by
later conflict checks.

Legacy non-UUID intents can still transition locally, but they are not
published to the DSS.

## Reconciliation and concurrency

The publication worker runs once per API process when publication is enabled.
PostgreSQL coordinates multiple replicas:

- `FOR UPDATE SKIP LOCKED` assigns different due records to different workers;
- time-bounded leases allow another worker to recover work after a crash;
- lease renewal protects a claimed record during a DSS mutation;
- publication revisions reject stale confirmations and stale failure writes;
- intent-scoped transactions serialize lifecycle and publication requests;
- a new request coalesces onto the existing publication while preserving the
  confirmed DSS identity needed for the next update or delete.

These mechanisms prevent two workers from authoritatively committing different
results for one revision. They do not make remote HTTP delivery exactly once;
recovery instead relies on OVNs, DSS reads, deterministic notification IDs,
and idempotent peer handling.

## Ambiguous remote outcomes

Network failure does not prove that a DSS mutation failed. The reconciler uses
the following recovery behavior:

| Ambiguous operation | Recovery |
| --- | --- |
| Create or update response lost | Read the DSS reference. If the desired state/version advanced, retain the recovered OVN and retry an update so a complete subscriber receipt can be obtained before confirmation. |
| Delete conflicts on stale OVN | Read the current reference, replace the local OVN, and retry the delete. |
| Delete response lost and retry returns `404` | Query subscribers over the published volumes, build deletion notifications, and only then confirm withdrawal. |
| Withdrawal requested while a create may be in flight | Read the DSS when the local OVN is empty; delete a recovered reference instead of assuming that no external reference exists. |
| Worker crashes after claiming work | The lease expires and another worker reclaims the same durable record. |
| Worker loses a publication revision race | The stale result is rejected; the newer desired state remains authoritative. |

An empty local OVN is never considered sufficient evidence that a DSS
reference does not exist when a mutation may have been in flight.

## USS-to-USS surface and security

Publication enables two SCD endpoints:

- `GET /uss/v1/operational_intents/{entity_id}?version={dss_version}` serves the
  details for the version currently confirmed by the DSS; and
- `POST /uss/v1/operational_intents` receives peer change or deletion
  notifications.

Both endpoints require a signed bearer JWT with the configured issuer,
audience, and `utm.strategic_coordination` scope. The notification sender must
also match the token subject. The GET endpoint never exposes an unpublished
draft or a pending replacement.

Peer USS URLs require HTTPS and addresses that are publicly routable by
default. Private, loopback, link-local, carrier-grade NAT, benchmarking,
documentation, and other special-use destinations are rejected. The insecure
URL option is only for a trusted local InterUSS environment.

The DSS bearer token is separate from peer USS verification. Aero Arc can
obtain a DSS token through the configured OAuth endpoint or use an externally
managed static token. Peer USSs authenticate inbound requests using the
configured public verification key.

## Operator visibility

`GET /api/v1/operational-intents/{intent_id}/coordination` exposes the
publication record. Operators should distinguish:

- local intent status;
- desired DSS state;
- confirmed DSS state and published intent version;
- synchronization status, attempt count, next attempt, and last error; and
- the current OVN and DSS version.

An intent is externally ready only when the expected version and state are
confirmed. `pending`, `processing`, `retrying`, and `blocked` must not be shown
as globally coordinated success.

## Current limits

The publication slice currently supports the WGS84 polygon and altitude forms
validated by the InterUSS adapter. Unsupported SCD geometry, altitude
references, malformed local geometry, peer failures, and antimeridian-crossing
geometry fail closed.

Before safety-critical production deployment, the system still needs:

- versioned database migrations and removal of all production memory fallbacks;
- production authentication, tenancy, authorization, and managed secrets;
- metrics and alerts for publication age, retries, leases, blocked work,
  indeterminate checks, and peer delivery age;
- multi-replica load and fault-injection tests against a staging DSS;
- recovery drills for crashes and ambiguous create, update, and delete results;
- operational procedures for inspecting and requeuing permanently blocked
  publication records; and
- conformance, telemetry-quality, and operator-response loops around active
  operations.

The broader PostGIS production backlog is tracked in
[Deconfliction PostGIS: Production Follow-up](deconfliction-postgis-next-steps.md).

## Suggested code-reading path

1. `internal/domain/coordination.go` defines publication and notification
   records.
2. `internal/service/intent_service.go` defines lifecycle orchestration and the
   activation authority boundary.
3. `internal/service/deconfliction/publication.go` defines reconciliation and
   peer delivery.
4. `internal/store/durable/durable.go` defines the transactional contracts.
5. `internal/store/durable/postgres/publication.go` implements leases,
   revisions, and outboxes.
6. `internal/service/deconfliction/publication_test.go` and
   `publication_integration_test.go` exercise recovery behavior.
