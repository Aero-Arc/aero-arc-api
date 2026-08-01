CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS spatial_operational_volumes (
    intent_id text NOT NULL,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    id text NOT NULL,
    altitude_reference text NOT NULL CHECK (altitude_reference <> ''),
    min_altitude_m double precision NOT NULL
        CHECK (min_altitude_m >= 0 AND min_altitude_m < 'Infinity'::double precision),
    max_altitude_m double precision NOT NULL
        CHECK (max_altitude_m > 0 AND max_altitude_m < 'Infinity'::double precision),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL CHECK (starts_at < ends_at),
    buffer_meters double precision NOT NULL DEFAULT 0 CHECK (buffer_meters >= 0),
    footprint geometry(Polygon, 4326),
    PRIMARY KEY (intent_id, intent_version, id),
    CHECK (min_altitude_m <= max_altitude_m),
    CHECK (footprint IS NULL OR ST_IsValid(footprint))
);

CREATE INDEX IF NOT EXISTS spatial_operational_volumes_footprint_idx
    ON spatial_operational_volumes USING gist ((footprint::geography));
CREATE INDEX IF NOT EXISTS spatial_operational_volumes_time_idx
    ON spatial_operational_volumes (starts_at, ends_at);
CREATE INDEX IF NOT EXISTS spatial_operational_volumes_intent_idx
    ON spatial_operational_volumes (intent_id, intent_version);
