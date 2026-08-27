CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS operational_intents (
    id text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    aircraft_id text NOT NULL,
    planned_start_at timestamptz NOT NULL,
    planned_end_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    PRIMARY KEY (id, version),
    CONSTRAINT operational_intents_planned_window_check
        CHECK (planned_start_at < planned_end_at)
);

-- Upgrade databases created by an earlier revision of this vertical slice.
-- Production schema evolution moves to versioned migrations before release.
ALTER TABLE operational_intents
    ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    ADD COLUMN IF NOT EXISTS planned_end_at timestamptz;
UPDATE operational_intents
SET planned_end_at = planned_start_at + interval '1 microsecond'
WHERE planned_end_at IS NULL;
ALTER TABLE operational_intents
    ALTER COLUMN planned_end_at SET NOT NULL;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'operational_intents'::regclass
          AND conname = 'operational_intents_planned_window_check'
    ) THEN
        ALTER TABLE operational_intents
            ADD CONSTRAINT operational_intents_planned_window_check
            CHECK (planned_start_at < planned_end_at);
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS operational_intents_aircraft_start_idx
    ON operational_intents (aircraft_id, planned_start_at);

CREATE UNIQUE INDEX IF NOT EXISTS operational_intents_one_active_aircraft_idx
    ON operational_intents (aircraft_id)
    WHERE data->>'status' = 'active';

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
    buffer_meters double precision NOT NULL DEFAULT 0
        CONSTRAINT operational_volumes_buffer_meters_check
        CHECK (buffer_meters >= 0 AND buffer_meters < 'Infinity'::double precision),
    footprint geometry(Polygon, 4326),
    data jsonb NOT NULL,
    PRIMARY KEY (intent_id, intent_version, id),
    FOREIGN KEY (intent_id, intent_version)
        REFERENCES operational_intents (id, version) ON DELETE CASCADE,
    CHECK (min_altitude_m <= max_altitude_m),
    CHECK (footprint IS NULL OR ST_IsValid(footprint))
);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'operational_volumes'::regclass
          AND conname = 'operational_volumes_buffer_meters_check'
          AND pg_get_constraintdef(oid) NOT LIKE '%Infinity%'
    ) THEN
        ALTER TABLE operational_volumes
            DROP CONSTRAINT operational_volumes_buffer_meters_check;
        ALTER TABLE operational_volumes
            ADD CONSTRAINT operational_volumes_buffer_meters_check
            CHECK (buffer_meters >= 0 AND buffer_meters < 'Infinity'::double precision);
    END IF;
END
$$;

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
    data jsonb NOT NULL,
    CONSTRAINT conflict_findings_intent_version_fkey
        FOREIGN KEY (intent_id, intent_version)
        REFERENCES operational_intents (id, version) ON DELETE CASCADE
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'conflict_findings'::regclass
          AND conname = 'conflict_findings_intent_version_fkey'
    ) THEN
        ALTER TABLE conflict_findings
            ADD CONSTRAINT conflict_findings_intent_version_fkey
            FOREIGN KEY (intent_id, intent_version)
            REFERENCES operational_intents (id, version) ON DELETE CASCADE;
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS conflict_findings_intent_rule_idx
    ON conflict_findings (intent_id, intent_version, rule_version, evaluated_at DESC);

CREATE TABLE IF NOT EXISTS operational_intent_publications (
    intent_id text PRIMARY KEY,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    desired_intent_version integer NOT NULL CHECK (desired_intent_version > 0),
    published_intent_version integer CHECK (published_intent_version > 0),
    desired_state text NOT NULL,
    sync_status text NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    lease_until timestamptz,
    updated_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    FOREIGN KEY (intent_id, desired_intent_version)
        REFERENCES operational_intents (id, version) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS operational_intent_publications_due_idx
    ON operational_intent_publications (next_attempt_at, lease_until)
    WHERE sync_status IN ('pending', 'processing', 'retrying');

CREATE TABLE IF NOT EXISTS peer_notifications (
    id text PRIMARY KEY,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    intent_id text NOT NULL,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    uss_base_url text NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    delivered_at timestamptz,
    lease_until timestamptz,
    updated_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    FOREIGN KEY (intent_id, intent_version)
        REFERENCES operational_intents (id, version) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS peer_notifications_due_idx
    ON peer_notifications (next_attempt_at, lease_until)
    WHERE delivered_at IS NULL;

CREATE TABLE IF NOT EXISTS received_peer_notifications (
    id text PRIMARY KEY,
    intent_id text NOT NULL,
    manager text NOT NULL,
    intent_version integer,
    received_at timestamptz NOT NULL,
    data jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS received_peer_notifications_intent_idx
    ON received_peer_notifications (intent_id, received_at);

CREATE TABLE IF NOT EXISTS aircraft (
    id text PRIMARY KEY,
    operator_id text NOT NULL,
    agent_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    data jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS aircraft_operator_idx ON aircraft (operator_id, id);

CREATE TABLE IF NOT EXISTS flight_records (
    id text PRIMARY KEY,
    operator_id text NOT NULL,
    aircraft_id text NOT NULL REFERENCES aircraft (id) ON DELETE RESTRICT,
    intent_id text NOT NULL,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    status text NOT NULL,
    started_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    FOREIGN KEY (intent_id, intent_version)
        REFERENCES operational_intents (id, version) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS flight_records_aircraft_idx
    ON flight_records (aircraft_id, started_at DESC, id);

CREATE TABLE IF NOT EXISTS missions (
    id text PRIMARY KEY,
    operator_id text NOT NULL,
    flight_id text NOT NULL,
    aircraft_id text NOT NULL,
    intent_id text NOT NULL,
    intent_version integer NOT NULL CHECK (intent_version > 0),
    version integer NOT NULL CHECK (version > 0),
    source_format text NOT NULL,
    source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
    mission_digest text NOT NULL CHECK (mission_digest ~ '^[0-9a-f]{64}$'),
    idempotency_key text NOT NULL UNIQUE,
    idempotency_request_hash text NOT NULL CHECK (idempotency_request_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    UNIQUE (flight_id, version),
    FOREIGN KEY (flight_id) REFERENCES flight_records (id) ON DELETE RESTRICT,
    FOREIGN KEY (aircraft_id) REFERENCES aircraft (id) ON DELETE RESTRICT,
    FOREIGN KEY (intent_id, intent_version)
        REFERENCES operational_intents (id, version) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS missions_flight_current_idx
    ON missions (flight_id, version DESC);
CREATE INDEX IF NOT EXISTS missions_intent_current_idx
    ON missions (aircraft_id, intent_id, intent_version, created_at DESC, version DESC);

CREATE TABLE IF NOT EXISTS mission_items (
    mission_id text NOT NULL REFERENCES missions (id) ON DELETE CASCADE,
    sequence integer NOT NULL CHECK (sequence >= 0 AND sequence < 200),
    data jsonb NOT NULL,
    PRIMARY KEY (mission_id, sequence)
);

CREATE TABLE IF NOT EXISTS mission_deployments (
    id text PRIMARY KEY,
    flight_id text NOT NULL,
    mission_id text NOT NULL REFERENCES missions (id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL UNIQUE,
    idempotency_request_hash text NOT NULL CHECK (idempotency_request_hash ~ '^[0-9a-f]{64}$'),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    status text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    data jsonb NOT NULL,
    FOREIGN KEY (flight_id) REFERENCES flight_records (id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS mission_deployments_flight_idx
    ON mission_deployments (flight_id, created_at DESC);
