# Migrations

PostgreSQL 14 with PostGIS 3.5 is the selected local target for the
deconfliction vertical slice. The PostgreSQL durable store initializes its
idempotent schema from `internal/store/durable/postgres/schema.sql` when
`AERO_API_DURABLE_STORE=postgres` is selected.

Operational intents, versions, volumes, conflict findings, and PostGIS search
columns live in the same authoritative tables. PostgreSQL updates the GiST
spatial index in the same transaction as each volume write, so horizontally
scaled API replicas do not coordinate an application-managed projection.

The other durable domain groups still use the embedded memory implementation
in this vertical slice. A versioned migration runner and PostgreSQL tables for
those groups should replace startup schema initialization and the fallback
before production deployment.
