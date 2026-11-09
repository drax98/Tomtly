CREATE TABLE script_grants (
    script text NOT NULL,
    key text NOT NULL,
    PRIMARY KEY (script, key)
);
