# Service bridge

For what a sandbox can't do (USB, Bluetooth, serial ports, native libraries), run a small helper service in any
language and let this extension turn it into devices. The protocol is small
(see [docs/devices.md](../../docs/devices.md#a-service-in-any-language)):

```
GET  /devices                 a JSON array of devices, as in the device model
POST /devices/{id}/dps/{dp}   body {"value": ...}; any 2xx means accepted
GET  /data                    optional: anything JSON can hold, readable as home.plugin("service-bridge").data()
```

A device has `id`, `name`, `kind`, `product`, `online` and `dps`, each `{dp, code, type, values, scale, writable, value}`.
The extension shows `code` to scripts and posts to `dp`. A helper that isn't reachable makes every device offline. A
helper that sends something it shouldn't (not an array, a device with no id) is an error on the card, not an empty home.

It runs as an extension: approved on the page, its network access is limited to the one address, and the token can only
go there. A tiny Python helper is in [examples/services](../../examples/services/README.md), and a Go one in
[examples/adapters/virtual-lamp](../../examples/adapters/virtual-lamp/main.go). For a second helper, copy the folder
and change its `id`.

## Status

Tested against a fake HTTP server through the real extension host (devices, scale, writes with escaped ids, `/data`,
bearer token, wrong token). Not tested against a real helper or any hardware.

## Access

`network: ["{host}"]`: only the helper's address from your setting, over plain HTTP. No sockets, no storage.

## Settings

| | |
|---|---|
| `host` | Where the helper listens, `host` or `host:port`. |
| `token` | Optional secret, sent as `Authorization: Bearer <token>` and only to `host`. |
| `data` | Also fetch `GET /data` every minute. Off by default, and the plugin data is `null` while off. |

```json
{ "source": "./extensions/service-bridge", "settings": { "host": "192.168.1.20:9000", "token": "${BRIDGE_TOKEN}", "data": true } }
```

## Limits

- The setting must be called `host` for the access list to follow it, so it takes an address, not a URL.
- Plain HTTP only. Use it on your own network. A helper on the internet needs an extension of your own that speaks HTTPS.
- Writes go to the helper and the page re-polls straight away. The helper decides whether it worked: answer a non-2xx
  status and the error shows on the page.

## Licence

MIT, like the rest of the repository.
