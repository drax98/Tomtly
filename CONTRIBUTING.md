# Contributing

Thanks for helping. This file says how to get a working copy, what has to pass, and what a change that gets merged looks
like.

## Before you start

- **A new integration or plugin** usually doesn't belong in this repo. Build it as an extension
  ([docs/extensions.md](docs/extensions.md)) and upload it to the marketplace, where it's reviewed and listed on its own.
- **A bug** goes in an issue first, with what you did, what happened and what you expected. Leave out device keys, tokens and
  addresses; the service never needs them to be reproduced.
- **A bigger change** (a new page, a new kind of access for extensions, anything in the sandbox or the marketplace
  checks) starts as an issue, so the shape can be agreed before the work.
- **A security problem** never goes in an issue. See [SECURITY.md](SECURITY.md).

## A working copy

You need Go (the version in `go.mod`), Node 22 with [pnpm](https://pnpm.io) (the version is pinned in each
`package.json`, and `corepack enable` picks it up), and Docker for Postgres and the end-to-end tests.

```bash
go test ./...                                   # the service and every extension's checks
cd web && pnpm install && pnpm test && pnpm dev # the page, with hot reload, proxying /api to a service on :8099
```

The Postgres store has its own tests that run only with a database:

```bash
docker run -d --name pg-test -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:17-alpine
docker exec pg-test psql -U postgres -c 'CREATE DATABASE home_control'
TEST_DATABASE_URL='postgres://postgres:test@localhost:5432/home_control?sslmode=disable' go test ./internal/store/
```

`cd web && pnpm e2e` drives every page in a real browser against real images (see the README).

### Mutation tests

`tools/mutate` changes one thing at a time in a Go file (a flipped comparison, `&&` for `||`, a dropped `!`, `true` for
`false`) and runs that package's tests against it, without touching the file. Each change the tests don't notice is a
missing test, or a change that can't matter. Run it on security-sensitive code you touch:

```bash
go run ./tools/mutate -pkg ./internal/auth internal/auth/password.go internal/auth/limiter.go
go run ./tools/mutate -pkg ./internal/extension -run 'Market' internal/extension/market.go
```

## What has to pass

CI runs all of this on every pull request:

- `gofmt`, `go vet ./...` and `go test ./...`, plus the Postgres tests above
- `pnpm test`, `pnpm build` and `pnpm test:extensions` in `web/`
- security: `govulncheck`, `gosec`, `pnpm audit` and CodeQL
- the Docker image builds
- the end-to-end suite

## What a good change looks like

- **Small and whole.** One change per pull request, with its tests, docs and every caller it touches. Don't leave a
  half-moved feature behind.
- **Tested where it can break.** New logic gets a test next to it: a Go `_test.go`, a Vitest file, or an end-to-end spec
  for a flow across pages.
- **Plain words on the page.** Text people read says what happens in everyday language, without internal names or jargon.
  The [extension style guide](docs/extension-style-guide.md) has the details, and it applies to the app too.
- **No new dependency for a few lines.** Use the standard library, or what's already in `go.mod` and `package.json`.
- **Comments say why**, not what the next line does.
- **Security is never the shortcut.** Validation at trust boundaries, the extension sandbox and approvals, and the
  marketplace signature checks stay as strict as they are.

The page uses [shadcn/ui](https://ui.shadcn.com) components and Tailwind; reuse what's in `src/components/ui` before
adding anything new, and check the page at phone width.

## Commits

Write the subject as what the change does, in the imperative, with the area first when it helps:
`Marketplace: show weekly downloads on a listing`. By contributing you agree your change is released under the
[MIT license](LICENSE).
