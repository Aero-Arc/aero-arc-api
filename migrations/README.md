# Migrations

This directory is reserved for durable store schema migrations.

The scaffold currently runs in `memory` mode. Future migrations should describe
the relational source of truth for aircraft, batteries, battery installations,
maintenance events, flight records, operational intents, and conformance events.

TODO: add TiDB/Postgres migration tooling once the production durable store is
selected.

