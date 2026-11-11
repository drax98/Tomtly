# Tomtly

[![CI](https://github.com/drax98/Tomtly/actions/workflows/ci.yml/badge.svg)](https://github.com/drax98/Tomtly/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

One local service for every smart device at home. It talks to devices directly over your LAN, gives them one API and one control panel, and runs your automations as TypeScript, so you don't depend on each vendor's cloud or app.

- **Integrations for any device.** Smart Life (Tuya), Xiaomi Home and Gree come as extensions from [their own repository](https://github.com/drax98/tomtly-extensions), and anything else is a TypeScript extension you write.
- **Automations as code.** Scripts run inside the service with nothing else to install, and start in a shadow mode that changes nothing until you've seen they do the right thing.
- **Extensions from anywhere.** Add devices or outside data like the weather from GitHub, a local folder or a file you upload, and build your own in TypeScript with the [SDK](docs/extensions.md). They run sandboxed, and only get the access they declare and you approve.
- **Login and roles.** Accounts with viewer, operator, editor and admin roles, API tokens and an audit log.
- **Runs anywhere.** A single static Go binary, or one container on any machine. Everything about your home stays in one folder outside this repo.

> **Status:** early and changing. Expect rough edges, and read [Limits](#limits) before you rely on it.

## Run

Each release publishes images for amd64 and arm64 to `ghcr.io/drax98/tomtly` (see [self-hosting](docs/self-hosting.md)). To build from source instead:

```bash
mkdir -p ~/.config/tomtly/secrets
cp -R examples/config.json examples/automations ~/.config/tomtly/
# edit config.json, and put your credentials files in secrets/ (see each integration's README)
cp .env.example .env
echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)" >> .env
docker compose up -d --build
open http://localhost:8099
```

The first visit asks you to create the admin account.

To run without Docker, you'll need your own Postgres. Build the page with `pnpm install && pnpm build` in `web/`, then:

```bash
DATABASE_URL=postgres://user:password@localhost:5432/tomtly CONFIG_DIR=~/.config/tomtly go run ./cmd/tomtly
```

## Your config folder

```
~/.config/tomtly/
  config.json     extensions, timezone, rooms and allowed host names
  automations/    your scripts, the only part the service can write to
  extensions/     extensions you develop, listed as ./extensions/<folder>
  secrets/        credentials files, and env for ${VARS}
```

Everything the service writes itself is in Postgres: accounts, which scripts are live, the activity and audit logs, extensions you add with their settings, approved access, blocked attempts, and uploaded or downloaded extension code. So `pg_dump` plus this folder is a complete backup. Treat the dump like your secrets, since extension settings are stored unencrypted.

Set `TOMTLY_DIR` in `.env` to keep it elsewhere. Values like `${TOKEN}` in `config.json` are read from the environment, which Docker fills from `secrets/env`, and the service refuses to start if one is unset.

`rooms` in `config.json` maps a room name to device ids such as `tuya:<id>`, for grouping on the page. It's optional, and rooms show in alphabetical order.

## Integrations

Every integration and plugin is an extension. The official ones are folders of [drax98/tomtly-extensions](https://github.com/drax98/tomtly-extensions). Add one on the **Integrations** or **Plugins**
page (**From a GitHub repo**, or from the marketplace's website, whose **Get** opens it here to install), or list it in
`config.json` under `extensions`, as [examples/config.json](examples/config.json) does.

| Folder | Devices | Protocol |
|---|---|---|
| [`tuya`](https://github.com/drax98/tomtly-extensions/tree/main/tuya) (Smart Life) | Tuya Zigbee and Wi-Fi | LAN port 6668, protocols 3.1 to 3.5 |
| [`xiaomi`](https://github.com/drax98/tomtly-extensions/tree/main/xiaomi) (Xiaomi Home) | Any Wi-Fi MIoT device from its spec, older Yeelight, Philips, Zhimi, Chuangmi and Aqara gateway models, Roborock vacuums | miIO, UDP port 54321 |
| [`gree`](https://github.com/drax98/tomtly-extensions/tree/main/gree) | Gree-based ACs such as Sinclair and EWPE Smart | UDP port 7000 |
| [`weather`](https://github.com/drax98/tomtly-extensions/tree/main/weather) | A plugin: weather and forecast from Open-Meteo | HTTPS |

Each folder's README says how to get its credentials, what it was tested against, and how it finds a device whose
address changed. Device ids are `<extension id>:<id>`, such as `tuya:<id>`, `xiaomi:<did>` or `gree:<mac>`. To write
your own, see [docs/extensions.md](docs/extensions.md). `config.json` no longer accepts `adapters` or `plugins`, and the
service refuses to start if they are there.

## Automations

Scripts are TypeScript files in your `automations/` folder, which you can edit on the page or in your own editor. See [docs/automations.md](docs/automations.md).

```ts
const plug = home.device("your-device-id");

home.daily("08:30", () => plug.set("switch", true));
home.daily("00:30", () => plug.set("switch", false));
```

## Extensions

Extensions add devices, data or actions without changing this repo. The integrations above are extensions too. Add one on the **Integrations** or **Plugins** page, or list it in `config.json`:

```json
"extensions": [
  { "source": "github:drax98/tomtly-extensions/weather@weather-v1.1.1", "settings": { "latitude": 52.23, "longitude": 21.01 } },
  { "source": "./extensions/my-own", "settings": {} }
]
```

You can also upload a `.zip`, `.tar.gz` or `extension.ts` on those pages. An extension only gets the access it declares, which you approve before it runs, and blocked attempts show on its card. The script editor suggests the real device ids, value codes and plugin fields of your home automatically. To build one, start with [docs/extensions.md](docs/extensions.md), the [API reference](docs/extension-api.md) and the [style guide](docs/extension-style-guide.md).

## Marketplace

A marketplace is a separate service, shared by many homes, that lists integrations and plugins a person has read and
approved. People browse it on its website and choose **Get**, which opens that listing in their own Tomtly: the
**Marketplace** page there checks it against the marketplace's signed list, shows what it can reach, and installs it only
when they agree. It also lists updates for what you have.

Nothing from a marketplace is trusted on its word. The list as a whole and each approved entry are signed with separate
keys, each download is checked against the exact hash a person approved, and an installed extension never needs the
marketplace again, though it hears if its version gets taken back. Approval never grants access for you: you still see
what an extension asks for and allow it.

To use one, set `MARKETPLACE_URL`, `MARKETPLACE_PUBLIC_KEY` and `MARKETPLACE_APPROVAL_PUBLIC_KEYS` in `.env`. Without them
Tomtly runs normally and the page says the marketplace isn't set up. To publish your own extension there, see
[docs/publishing.md](docs/publishing.md); to run a marketplace of your own, [docs/marketplace-protocol.md](docs/marketplace-protocol.md)
is the contract every home checks.

## API

| | |
|---|---|
| `GET /api/devices` | Every device with its current values |
| `POST /api/devices/{id}/dps/{dp}` | Change a value, with body `{"value": ...}` in raw device units |
| `GET /api/rooms` | Rooms from `config.json` |
| `GET /api/widgets` | What's on Home (`layout`, in order) and every widget scripts and extensions offer, as last drawn |
| `PUT /api/dashboard` | Arrange Home, with body `{"widgets": ["builtin/devices", "script:heat/panel", "ext:weather/now"]}`; up to 30 |
| `POST /api/widgets/{owner}/{key}/actions/{action}` | Press a widget's button, with body `{"input": {...}}`; answers whether the script is live |
| `GET /api/automations` | Scripts, recent activity and plugin data |
| `POST /api/automations/{name}/live` | Switch a script, with body `{"live": true}` |
| `POST /api/automations/reload` | Reload scripts from disk, with body `{}` |
| `GET /api/automations/{name}/source` | A script's text and version |
| `POST /api/automations/{name}/source` | Save a script, with body `{"source": ..., "version": ..., "keepLive": false}`; an empty version creates one |
| `GET /api/automations/sdk` | Types, for editors |
| `GET /api/integrations` | Every service with its devices |
| `GET /api/extensions`, `POST`, `DELETE` | Extensions, see [the reference](docs/extension-api.md#http-api) |

## Develop

```bash
go test ./...
cd web && pnpm install && pnpm test && pnpm dev
```

`pnpm dev` serves the page with hot reload and proxies `/api` to a service on port 8099. The page uses [shadcn/ui](https://ui.shadcn.com), so add components with `pnpm dlx shadcn@latest add <name>`.

### End-to-end tests

`cd web && pnpm install && pnpm e2e` drives every page in a real browser, against the real service image and Postgres. It only needs Docker: the browser runs in the Playwright image matching `@playwright/test` in `web/package.json`. Pass any Playwright arguments, like `pnpm e2e specs/08-devices.spec.ts`, or `pnpm e2e --debug` to also see the page on `127.0.0.1:18099` until Ctrl-C. `pnpm e2e --no-build` reuses the last images when only specs or fixtures changed.

It exits non-zero on a failure and leaves `web/e2e/.results/report` (`pnpm exec playwright show-report e2e/.results/report`) with a trace for each test. It runs as its own compose project, `tomtly-e2e`, with its own networks and no published ports, and removes its containers, volumes and networks afterwards, keeping only the images for next time. Its config is `web/e2e/fixtures`, never yours.

Only GitHub is faked, for adding an extension from a repo. Devices are a fake on a second network that the service reaches through a router, since extensions can't reach the service's own network.

## Login and roles

Every API call needs signing in, or an API token sent as `Authorization: Bearer <token>`. Automations keep running whether or not anyone is signed in.

| Role | Can |
|---|---|
| Viewer | See devices, automations and extensions |
| Family (`operator` in the API) | Also control devices |
| Editor | Also edit scripts and switch them live |
| Administrator (`admin`) | Also approve what automations may reach, manage extensions and accounts, and read the audit log |

If you lose your admin password, this prints a new one:

```bash
docker compose exec tomtly /tomtly reset-admin <username>
```

## Security

The first account is made on the page. From anywhere but `localhost` on this machine it takes a one-time setup code, printed in the logs when no account exists: `docker compose logs tomtly`.

If a reverse proxy is in front, set `TRUSTED_PROXIES` in `.env` to its address or range, or every sign-in looks like it comes from the proxy. Its `X-Forwarded-For` is believed only from there.

The port is bound to `127.0.0.1` by default. Before setting `BIND_ADDRESS` to expose it to your LAN or phone, put it behind a reverse proxy with HTTPS, because passwords and session cookies would otherwise travel unencrypted.

The API never returns local keys, tokens, password hashes or extension settings, and scripts can't import them. Requests from other websites are refused, and the API only answers to `localhost`, IP addresses and any names you list in `allowedHosts` in `config.json`, which you'll need behind a reverse proxy.

On Linux, the container runs as user 65534, so that user needs write access to `automations/` for the page to save: `sudo chown -R 65534 ~/.config/tomtly/automations`.

## Limits

- Pairing new devices still needs the vendor's app, followed by a fresh credentials file.
- A write returns as soon as it's queued. A refused command only shows up in the logs, and the page shows the device's real value after the next poll.
- A device that gets a new address is searched for only in its own /24 network (at most 254 addresses per scan); each integration's README says how it proves the device is the same one.
- Which Tuya data points are read-only is inferred from category and code names, because the cloud mapping doesn't say.
- An extension that listens on a port or takes webhooks is reachable by whoever can reach it, and from Docker the port has to be published in `compose.yaml`. Webhooks arrive on the service's ordinary port.
- Changing what's in `extensions` in `config.json`, or an extension's code, needs a restart.
- The service has to be running for automations to run. Rules you move out of a vendor's cloud or gateway depend on the machine hosting it.

## Credits

The Tuya extension's protocol is ported to TypeScript from [TinyTuya](https://github.com/jasonacox/tinytuya). The weather extension's data is by [Open-Meteo](https://open-meteo.com), under CC BY 4.0.

## Documentation

| | |
|---|---|
| [Integrations](#integrations) | Tuya, Xiaomi, Gree and weather, in [tomtly-extensions](https://github.com/drax98/tomtly-extensions) |
| [Automations](docs/automations.md) | Writing scripts, shadow mode, widgets |
| [Extensions](docs/extensions.md) | Building your own integration or plugin, with the [API reference](docs/extension-api.md) and [style guide](docs/extension-style-guide.md) |
| [Devices](docs/devices.md) and [plugins](docs/plugins.md) | What a device and a plugin extension look like |
| [Publishing](docs/publishing.md) | Putting your extension on the marketplace |
| [Marketplace protocol](docs/marketplace-protocol.md) | What a marketplace serves and what every home checks, to build a compatible one |
| [Self-hosting](docs/self-hosting.md) | Running it for real: HTTPS, backups, updates |

## Contributing

Issues and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) says how to build, test and what a good change
looks like. To publish an integration rather than change this repo, upload it to the marketplace instead.

Report security problems privately, as [SECURITY.md](SECURITY.md) describes, not in an issue.

## License

[MIT](LICENSE). The bundled extensions are MIT too, so each can be listed in the marketplace.
