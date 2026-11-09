CREATE TABLE users (
    id text PRIMARY KEY,
    username text NOT NULL,
    hash text NOT NULL,
    role text NOT NULL,
    disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX users_username ON users (lower(username));

CREATE TABLE sessions (
    hash text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    last_seen timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE api_tokens (
    id text PRIMARY KEY,
    seq bigserial NOT NULL,
    hash text NOT NULL UNIQUE,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name text NOT NULL,
    role text NOT NULL,
    created_at timestamptz NOT NULL,
    last_used timestamptz
);
CREATE INDEX api_tokens_user ON api_tokens (user_id);

CREATE TABLE audit (
    id bigserial PRIMARY KEY,
    time timestamptz NOT NULL,
    username text NOT NULL,
    action text NOT NULL,
    detail text NOT NULL,
    ip text NOT NULL
);
