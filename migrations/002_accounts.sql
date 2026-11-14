BEGIN;

-- Demo reservations from migration 001 can reference users 1 and 2.
CREATE TABLE accounts (
    id bigint GENERATED ALWAYS AS IDENTITY (START WITH 1000) PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_salt bytea NOT NULL,
    password_hash bytea NOT NULL,
    role text NOT NULL DEFAULT 'guest' CHECK (role IN ('guest', 'admin'))
);

CREATE TABLE sessions (
    access_hash bytea PRIMARY KEY,
    refresh_hash bytea NOT NULL UNIQUE,
    account_id bigint NOT NULL REFERENCES accounts ON DELETE CASCADE,
    access_expires_at timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL
);

COMMIT;
