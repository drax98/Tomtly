-- What the owner changed about a device on the page: a name of their own, and a room. A null room follows config.json,
-- and an empty one takes the device out of every room.
CREATE TABLE device_overrides (
    device_id text PRIMARY KEY,
    name text NOT NULL DEFAULT '',
    room text
);
