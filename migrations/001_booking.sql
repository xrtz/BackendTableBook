BEGIN;

CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;

CREATE TABLE halls (
    id bigint PRIMARY KEY,
    name text NOT NULL,
    time_zone text NOT NULL,
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0)
);

CREATE TABLE dining_tables (
    id bigint PRIMARY KEY,
    hall_id bigint NOT NULL REFERENCES halls,
    label text NOT NULL,
    capacity integer NOT NULL CHECK (capacity > 0),
    shape text NOT NULL CHECK (shape IN ('round', 'square')),
    x integer NOT NULL CHECK (x >= 0),
    y integer NOT NULL CHECK (y >= 0),
    size integer NOT NULL CHECK (size > 0),
    UNIQUE (hall_id, label)
);

CREATE TABLE reservations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL CHECK (user_id > 0),
    table_id bigint NOT NULL REFERENCES dining_tables,
    guest_count integer NOT NULL CHECK (guest_count > 0),
    starts_at timestamptz NOT NULL CHECK (isfinite(starts_at)),
    ends_at timestamptz NOT NULL CHECK (isfinite(ends_at)),
    status text NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('confirmed', 'seated', 'completed', 'cancelled')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    occupied_at tstzrange GENERATED ALWAYS AS (tstzrange(starts_at, ends_at, '[)')) STORED,
    CHECK (ends_at - starts_at = interval '2 hours'),
    CONSTRAINT reservations_table_interval_excl
        EXCLUDE USING gist (table_id WITH =, occupied_at WITH &&)
        WHERE (status IN ('confirmed', 'seated', 'completed'))
);

CREATE TABLE reservation_create_keys (
    user_id bigint NOT NULL CHECK (user_id > 0),
    key text NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    table_id bigint NOT NULL,
    guest_count integer NOT NULL,
    starts_at timestamptz NOT NULL,
    response jsonb,
    PRIMARY KEY (user_id, key)
);

COMMIT;
