-- Rooms in the order the page shows them. Which room a device is in is in device_overrides.
CREATE TABLE rooms (
    name text PRIMARY KEY,
    position integer NOT NULL
);
-- One row once config.json's rooms were copied in. From then on the page manages rooms.
CREATE TABLE rooms_seeded (
    only_row boolean PRIMARY KEY DEFAULT true CHECK (only_row)
);
