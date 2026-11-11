-- Extension settings hold keys and credential files, so they are kept encrypted. The service encrypts the rows written
-- before this into sealed when it starts, and settings stays empty from then on.
ALTER TABLE added_extensions ADD COLUMN sealed bytea;
ALTER TABLE added_extensions ALTER COLUMN settings DROP NOT NULL;
