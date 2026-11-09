-- The key this install signs its marketplace reviews with. One row, made the first time someone posts one.
CREATE TABLE install_key (
    only_row boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    key bytea NOT NULL
);
