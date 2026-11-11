# Self-hosting

The README's **Run** section gets Tomtly going on one machine. This page is for keeping it running: a released image,
HTTPS for your phone, backups and updates.

## A released image

Each release publishes `ghcr.io/drax98/tomtly` for amd64 and arm64 (a Raspberry Pi 4 or 5 works). Set it in `.env`:

```bash
TOMTLY_IMAGE=ghcr.io/drax98/tomtly:1.0   # a minor version: you get its fixes, never the next minor's changes
```

then `docker compose pull && docker compose up -d`. Leave it blank to build from your checkout with
`docker compose up -d --build`.

## Reaching it from your phone

The port is on `127.0.0.1` by default, because passwords and session cookies must not cross your network unencrypted.
To use it from other devices, put an HTTPS reverse proxy in front. With [Caddy](https://caddyserver.com) on the same
machine:

```
home.example.com {
    reverse_proxy 127.0.0.1:8099
}
```

Then tell Tomtly about it:

- `allowedHosts` in `config.json` lists the name, `["home.example.com"]`. The API answers only to `localhost`, IP addresses
  and these names.
- `TRUSTED_PROXIES` in `.env` is the address the proxy connects from, as the service sees it, so sign-in limits and the
  audit log see real addresses. Behind Docker's published port that is usually the bridge gateway, such as `172.17.0.1`;
  sign in once and check the address in the audit log to be sure.
- If you set `BIND_ADDRESS` to a LAN address instead of using a proxy on the same machine, also set `HOST_IPS` to this
  machine's LAN address, so extensions can't reach the service through it.

A name only your home network resolves still needs a certificate: Caddy's internal CA, or a DNS challenge for a real
domain, both work.

## Backups

Everything Tomtly writes is in Postgres; everything you write is in your config folder. Back up both:

```bash
docker compose exec -T postgres pg_dump -U tomtly tomtly | gzip > tomtly-$(date +%F).sql.gz
tar -czf tomtly-config-$(date +%F).tar.gz -C ~/.config tomtly
```

Treat both like passwords: extension settings in the dump and `secrets/` in the folder are not encrypted. To restore,
start a fresh stack, stop `tomtly`, and load the dump with
`gunzip -c tomtly-….sql.gz | docker compose exec -T postgres psql -U tomtly tomtly`.

## Updates

Read the release notes, back up, then pull the new image (or `git pull` and rebuild) and `docker compose up -d`. The
database is migrated on start, in one transaction; a failed migration leaves the old schema and the service refuses to
start, so the previous image still works with it. Going back a version after a migration ran isn't supported: restore the
backup instead.

## Is it up?

`GET /healthz` answers `ok` without an account, for uptime monitors. The image's own health check calls it, so
`docker compose ps` shows `healthy` once the service answers.

## Lost admin password

```bash
docker compose exec tomtly /tomtly reset-admin <username>
```

prints a new one.

## Running a marketplace

Homes don't need one. To run your own for a group of homes, follow [marketplace-protocol.md](marketplace-protocol.md): it says what a
marketplace serves, how it signs it, and what every home checks.
