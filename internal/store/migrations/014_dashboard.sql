-- The widgets on Home, in order, as "<owner>/<key>". One row, which the whole home shares. No row: never arranged.
CREATE TABLE dashboard (
    id int PRIMARY KEY CHECK (id = 1),
    widgets jsonb NOT NULL
);
