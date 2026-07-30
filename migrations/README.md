# Migrations

PostgreSQL 14 with PostGIS 3.5 is the selected local target for the
deconfliction vertical slice. The operational-intent slice initializes its
idempotent schema from `internal/store/durable/postgis/schema.sql` when a
database URL is configured. It owns operational intent versions, 4D volume
search columns, PostGIS footprints, and conflict findings.

The remaining scaffold records still use the configured general durable store.
A versioned migration runner should replace startup schema initialization before
the schema needs non-additive changes or production deployments remove runtime
DDL privileges.
