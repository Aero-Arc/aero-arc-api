# Migrations

PostgreSQL 14 with PostGIS 3.5 is the selected local target for the
deconfliction vertical slice. The application still runs its durable store in
`memory` mode and does not yet include a migration runner.

The first migration set should:

- enable the `postgis` extension;
- create version-aware operational intent records;
- store operational volumes as SRID 4326 `geometry(Polygon, 4326)` values;
- retain altitude reference, altitude band, and half-open time-window fields;
- persist conflict findings by intent ID, intent version, and rule version;
- add GiST spatial indexes and supporting time/status indexes; and
- create the remaining durable-store tables required by the `durable.Store`
  interface.

Migrations must complete before the API starts using the PostgreSQL durable
store. The Compose image initializes PostGIS for local development, but schema
creation should remain explicit and version-controlled here.

Migration tooling and the production upgrade/rollback policy still need to be
selected before the first schema is added.
