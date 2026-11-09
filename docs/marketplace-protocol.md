# The marketplace protocol

Home Control can install extensions from a marketplace: a separate service that many homes share. This page is the
contract between them: what a marketplace serves, in what shape, and what each home checks before it trusts any of it.
Build a service that follows it and every Home Control can use it, by setting three values in its `.env`.

The official marketplace's server isn't part of this repository. Everything a home relies on is: the checks below live in
[`internal/extension`](../internal/extension) (`market*.go`), and its tests drive them against fake marketplaces that
break every rule here on purpose. Read those tests next to this page; where the two disagree, the code is right.

## Trust: two kinds of key

A home trusts nothing a marketplace sends until it checks two signatures, both Ed25519:

- **Catalog keys** sign the whole list, so a home knows it came from that marketplace and is current. The service needs
  the private half to sign as it updates the list.
- **Approval keys** sign each entry, so a home knows a person approved that exact package. Keep the private half off the
  server: sign approvals somewhere else (the official marketplace signs them in the reviewer's browser). Then someone who
  takes over the server can stop listings, but can't add one or change what one installs.

A home is set up with:

```bash
MARKETPLACE_URL=https://market.example.com
MARKETPLACE_PUBLIC_KEY=<catalog public keys>             # base64 of the 32-byte key; several separated by commas
MARKETPLACE_APPROVAL_PUBLIC_KEYS=<approval public keys>  # the same form
```

Listing several keys lets you rotate one: sign with the new key once every home lists it, then drop the old one.

`MARKETPLACE_URL` must be `https`, unless it's on the home's own network (a private or loopback address, a name with no
dot, or `host.docker.internal`).

## What a home asks for

Every answer it reads must be `200 OK`. Anything else counts as "can't be reached right now", and what's installed keeps
working. All paths are under `MARKETPLACE_URL`.

| Request | Answer | Limit | Time |
|---|---|---|---|
| `GET /v1/catalog` | the signed list, below | 4 MiB | 15 s |
| `GET /v1/packages/<id>-<version>.tar.gz` | the package's exact bytes | 16 MiB | 60 s |
| `GET /v1/assets/<id>/<version>/<name>` | one image's exact bytes | 256 KiB icon, 2 MiB screenshot | 20 s |
| `GET /v1/reviews/<id>?limit=20&offset=<n>` | a page of reviews | 1 MiB | 10 s |
| `POST /v1/reviews/<id>` | takes a review a home signed | | 10 s |

A home asks for the catalog about once an hour, to hear about updates and versions taken back, and whenever its owner opens
a listing or installs. It reuses a catalog it verified in the last minute for that listing's images and reviews. It never sends anything about the home: no names, devices or addresses. Only a review
carries anything, and only what the person typed.

## `GET /v1/catalog`

```json
{
  "payload": "<base64 of the list's JSON>",
  "signature": "<base64 Ed25519 signature over those exact bytes, by a catalog key>",
  "ratings": { "<id>": { "average": 4.5, "count": 12 } },
  "downloads": { "<id>": 1266 }
}
```

The signature covers the **exact bytes** of `payload`, so a home never has to rebuild JSON the way the service wrote it.
`ratings` and `downloads` sit beside the signature: a home shows them, but nothing vouches for them, and either may be
left out.

The list inside `payload`:

```json
{
  "generatedAt": "2026-11-06T12:00:00Z",
  "signedAt": "2026-11-06T12:00:00Z",
  "expiresAt": "2026-11-13T12:00:00Z",
  "entries": [{ "entry": "<base64 of one entry's JSON>", "signature": "<base64, by an approval key>" }],
  "revoked": [{ "id": "lamp", "version": "1.0.0", "state": "suspended", "reason": "A report came in" }]
}
```

A home refuses the whole list, and offers nothing from it, when:

- the signature isn't by one of its catalog keys;
- any of the three times is missing, or `expiresAt` has passed. Sign again well before it expires (the official
  marketplace signs a week ahead). An expired list means the service stopped, someone is holding newer lists back, or
  the home's clock is wrong;
- `generatedAt` or `signedAt` is earlier than in a list the home already accepted. Homes remember the newest across
  restarts, so an old list can't be replayed to bring back something that was taken back.

Each entry is checked on its own. One that fails is left out, as if it weren't listed, and the rest stay.

### An entry

```json
{
  "id": "tides",
  "name": "Tide times",
  "description": "High and low water for any harbour.",
  "kind": "plugin",
  "version": "1.1.0",
  "author": "anna",
  "homepage": "",
  "license": "MIT",
  "categories": ["weather"],
  "tags": ["tides", "sea"],
  "provides": ["data"],
  "access": ["Internet: tides.example.org"],
  "testedOn": "A real tide gauge, firmware 2.1",
  "approvedOn": "2026-11-05",
  "sha256": "<64 lower-case hex digits>",
  "size": 532,
  "package": "/v1/packages/tides-1.1.0.tar.gz",
  "repository": "https://github.com/anna/tides",
  "migrations": [{ "version": "1.1.0", "note": "Needs a token now.", "ask": ["token"] }],
  "icon": { "name": "icon.png", "type": "image/png", "sha256": "<hex>" },
  "screenshots": [{ "name": "screenshot-1.png", "type": "image/png", "sha256": "<hex>" }]
}
```

| Field | Rule |
|---|---|
| `id` | `^[a-z0-9][a-z0-9-]{0,63}$`, the `id` in the package's `defineExtension` |
| `version` | `^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`, semver in practice; a higher one is an update |
| `sha256` | the SHA-256 of the package's exact bytes, 64 lower-case hex digits |
| `package` | exactly `/v1/packages/<id>-<version>.tar.gz`: an entry can't point anywhere else |
| `size` | 0 to 16 MiB |
| `kind` | `integration` if it provides only `devices`, `both` if `devices` and more, else `plugin` |
| `provides`, `access` | what the package's code declares. A home loads the code itself and refuses the install if its access differs from `access` |
| `license` | an SPDX id from the open licenses in [extension-api.md](extension-api.md); every version needs one, and a `LICENSE` file |
| `categories` | at most 10, from `climate`, `lighting`, `cleaning`, `energy`, `security`, `sensors`, `media`, `weather`, `bridges`, `other` |
| `tags` | at most 20, each up to 40 characters, no line breaks |
| `approvedOn` | `YYYY-MM-DD` |
| `testedOn` | what the reviewer ran it against, in words; shown on the listing |
| `repository`, `homepage` | `http` or `https` links, or empty. Nothing checks them: what was verified is the package |
| `migrations` | the package's migration files, shown before the update; the format is in [publishing.md](publishing.md#new-versions) |
| `icon` | optional; `name` is `icon.png`, `icon.jpg`, `icon.jpeg` or `icon.webp` |
| `screenshots` | at most 5; `name` matches `^screenshot-[A-Za-z0-9_-]{1,40}\.(png\|jpe?g\|webp)$` |
| image `type` | `image/png`, `image/jpeg` or `image/webp`, and it must be what the bytes really are. SVG is never accepted: it can carry script |

### Taking a version back

`revoked` lists versions homes should stop trusting:

- **`suspended`**: no new installs while it's looked into. Homes that have it see a warning with the reason, and it keeps
  running: a marketplace must never be able to switch a home off.
- **`withdrawn`**: gone for good. The same warning, and the listing leaves the catalog.

Leaving an entry out of `entries` takes it off the list; `revoked` is how homes that already have it find out why.

## `GET /v1/packages/<id>-<version>.tar.gz`

The bytes whose SHA-256 is the entry's `sha256`. A home throws away a download that doesn't match. The package is a
gzip-compressed tar with **one top-level folder** holding the extension: `extension.ts` and anything it imports,
`LICENSE`, `README.md`. A home unpacks only plain files and folders: links, absolute paths and `..` are refused, and so is
more than 5,000 entries, folders nested over 32 deep, or more than 64 MiB unpacked.

The same version must always be the same bytes: approvals pin the hash, so a version number is never reused for other
content.

## `GET /v1/assets/<id>/<version>/<name>`

An image named in the entry, whose SHA-256 must match the entry's and whose content must be the entry's `type`. Homes
fetch images through themselves, so the page never loads anything from the marketplace directly.

## Reviews

`GET /v1/reviews/<id>?limit=20&offset=0`:

```json
{
  "reviews": [{ "id": "a1", "rating": 5, "text": "Lovely", "name": "Ola", "at": "2026-11-04T12:59:25Z" }],
  "average": 4.5,
  "count": 12
}
```

Newest first. `text` and `name` are shown as plain text, never as markup.

`POST /v1/reviews/<id>` with `Content-Type: application/json`:

```json
{ "key": "<base64 Ed25519 public key>", "payload": "<base64 of the review's JSON>", "signature": "<base64 over those bytes>" }
```

The payload is `{"listing": "<id>", "rating": 1-5, "text": "...", "name": "...", "timestamp": <unix seconds>}`. Text is at
most 2,000 characters and the name 60, both valid UTF-8 without control characters (text may hold line breaks and tabs).

Each home makes its own key the first time it posts, and only for reviews, so the key is the home's pseudonymous identity.
A marketplace should keep one review per key and listing (a new one replaces the old), refuse a timestamp that isn't newer
than the last from that key (that's what a replayed post looks like), limit how often an address may post, and never
publish the key.

How a home reads the answer:

| Status | Meaning to the person |
|---|---|
| `200`, `201`, `204` | posted |
| `404` | the listing isn't there any more |
| `401` | the signature wasn't accepted: usually the home's clock is wrong |
| `409` | the marketplace already has a newer review from this home |
| `429` | too many reviews from here; try later |
| `400`, `413` | the marketplace didn't accept it |

## The website's Get button

A marketplace's website can send someone to their own Home Control to install a listing. Open
`<their Home Control>/marketplace/<id>`, with the address they typed. The website never talks to Home Control: Home Control
looks the id up in the catalog it verified itself, asks them to sign in as an administrator, and shows what the extension
can reach before anything is installed. Keep that address in the visitor's browser; it's an internal name of their home.

## Running a marketplace well

The protocol keeps homes safe from a dishonest or broken marketplace. These keep the marketplace itself worth trusting,
and they're what the official one does:

- **Review the exact package.** Read every file of the version you're about to approve, run it against real devices, and
  approve its hash, not a repository. Derive the entry from the stored package, never from what the author typed.
- **Run uploaded code elsewhere.** Loading an extension to read what it declares runs its top-level code: do it in a
  separate process with no keys, no database access, and limits on time and memory.
- **Sign approvals off the server**, and require two-step sign-in for everyone who can approve.
- **Only open source.** Every listed version names an open license and ships its text, so anyone can read what they install.
- **Never reuse a version number**, and keep every package you ever listed, so a home can always reinstall what it had.
