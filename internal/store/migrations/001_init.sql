CREATE TABLE script_live (
    name text PRIMARY KEY,
    live boolean NOT NULL
);

CREATE TABLE events (
    id bigserial PRIMARY KEY,
    time timestamptz NOT NULL,
    script text NOT NULL,
    kind text NOT NULL,
    message text NOT NULL
);
CREATE INDEX events_time ON events (time);

CREATE TABLE added_extensions (
    source text PRIMARY KEY,
    seq bigserial NOT NULL,
    settings jsonb NOT NULL
);

CREATE TABLE grants (
    extension text PRIMARY KEY,
    grants text[] NOT NULL
);

CREATE TABLE violations (
    id bigserial PRIMARY KEY,
    time timestamptz NOT NULL,
    extension text NOT NULL,
    kind text NOT NULL,
    detail text NOT NULL
);
CREATE INDEX violations_extension ON violations (extension, id);

CREATE TABLE extension_storage (
    extension text NOT NULL,
    key text NOT NULL,
    value jsonb NOT NULL,
    PRIMARY KEY (extension, key)
);

CREATE TABLE packages (
    key text PRIMARY KEY,
    archive bytea NOT NULL
);
