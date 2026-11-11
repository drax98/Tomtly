-- The home's own settings: one row, which the whole home shares.
CREATE TABLE home_settings (
    id int PRIMARY KEY CHECK (id = 1),
    language text NOT NULL DEFAULT '',
    timezone text NOT NULL DEFAULT '',
    latitude double precision,
    longitude double precision
);
