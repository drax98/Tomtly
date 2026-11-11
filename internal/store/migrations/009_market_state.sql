-- What this home last saw from the marketplace, kept so a restart can't be used to replay an old list. One row.
CREATE TABLE market_state (
    only_row boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    newest timestamptz NOT NULL,
    newest_signed timestamptz NOT NULL,
    revoked jsonb NOT NULL
);
