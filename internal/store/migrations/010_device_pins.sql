-- The values of each device pinned to its card, in the order they show. The whole home sees the same cards.
CREATE TABLE device_pins (
    device_id text PRIMARY KEY,
    dps jsonb NOT NULL
);
