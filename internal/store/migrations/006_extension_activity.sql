CREATE TABLE extension_activity (
    extension text NOT NULL,
    kind text NOT NULL,
    destination text NOT NULL,
    count bigint NOT NULL,
    first_seen timestamptz NOT NULL,
    last_seen timestamptz NOT NULL,
    PRIMARY KEY (extension, kind, destination)
);
