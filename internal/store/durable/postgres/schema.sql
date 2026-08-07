CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS operational_intents (
    id text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    aircraft_id text NOT NULL,
    planned_start_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS operational_intents_aircraft_start_idx
    ON operational_intents (aircraft_id, planned_start_at);

CREATE TABLE IF NOT EXISTS operational_volumes (
    intent_id text NOT NULL,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    id text NOT NULL,
    sequence integer NOT NULL,
    altitude_reference text NOT NULL CHECK (altitude_reference <> ''),
    min_altitude_m double precision NOT NULL
        CHECK (min_altitude_m >= 0 AND min_altitude_m < 'Infinity'::double precision),
    max_altitude_m double precision NOT NULL
        CHECK (max_altitude_m > 0 AND max_altitude_m < 'Infinity'::double precision),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL CHECK (starts_at < ends_at),
    buffer_meters double precision NOT NULL DEFAULT 0 CHECK (buffer_meters >= 0),
    footprint geometry(Polygon, 4326),
    data jsonb NOT NULL,
    PRIMARY KEY (intent_id, intent_version, id),
    FOREIGN KEY (intent_id, intent_version)
        REFERENCES operational_intents (id, version) ON DELETE CASCADE,
    CHECK (min_altitude_m <= max_altitude_m),
    CHECK (footprint IS NULL OR ST_IsValid(footprint))
);

CREATE INDEX IF NOT EXISTS operational_volumes_footprint_idx
    ON operational_volumes USING gist ((footprint::geography));
CREATE INDEX IF NOT EXISTS operational_volumes_time_idx
    ON operational_volumes (starts_at, ends_at);
CREATE INDEX IF NOT EXISTS operational_volumes_intent_idx
    ON operational_volumes (intent_id, intent_version, sequence);

CREATE TABLE IF NOT EXISTS conflict_findings (
    id text PRIMARY KEY,
    intent_id text NOT NULL,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    rule_version text NOT NULL,
    evaluated_at timestamptz NOT NULL,
    data jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS conflict_findings_intent_rule_idx
    ON conflict_findings (intent_id, intent_version, rule_version, evaluated_at DESC);
