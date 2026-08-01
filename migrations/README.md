# Migrations

PostgreSQL 14 with PostGIS 3.5 is the selected local target for the
deconfliction vertical slice. The spatial index initializes its idempotent
schema from `internal/spatialindex/postgis/schema.sql` when PostGIS is selected.
It owns a projection of 4D volume search columns and PostGIS footprints; it is
not the authoritative operational-intent database.

Operational intents, versions, volumes, and conflict findings remain in the
configured durable store. A versioned migration runner should replace startup
schema initialization before the spatial schema needs non-additive changes or
production deployments remove runtime DDL privileges.
