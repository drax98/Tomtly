# Devices

An integration connects a family of devices. Everything above it, the API, the page and automations, works only with the
device model below, so a new integration gets all of them for free.

Every integration is an [extension](extensions.md). Start from `extensions/hue` for a cloud or HTTP device, or from
`extensions/tuya`, `extensions/xiaomi` or `extensions/gree` for a LAN protocol over TCP or UDP. For what a sandbox can't
do, write a service in any language and connect it with `extensions/service-bridge`, as described [below](#a-service-in-any-language).

## The device model

A **device** has an `id`, a `name`, a `kind`, an `online` flag and a list of **data points**:

```json
{
  "id": "lamp",
  "name": "Desk lamp",
  "kind": "light",
  "category": "",
  "product": "Acme L1",
  "online": true,
  "dps": [
    { "dp": "1", "code": "switch", "type": "Boolean", "values": {}, "scale": 0, "writable": true, "value": false }
  ]
}
```

- **`id`** must be stable across restarts and unique. Scripts refer to devices by it.
- **`dp`** is how the integration addresses the value. **`code`** is the name scripts and the page use, and must be unique within the device.
- **`type`** is `Boolean`, `Enum`, `Integer` or `Action`. Other types can be read but not written.
- **`values`** holds the limits: `range` for `Enum` and `Action`, `min`, `max`, `step` and `unit` for `Integer`.
- **`scale`** says how to display an integer: `205` with scale `1` reads as `20.5`.
- **`value`** is `null` while unknown. Never invent a value, because scripts treat unknown differently from off.
- An `Action` is a command with no state, such as `start`. `value` stays `null`.

### Kinds and codes the page understands

Set `kind` to get a dedicated card. Any other kind, including your own, gets its own group and a generic card that
shows every data point with the right control, so nothing here is required.

| Kind | Card uses these codes |
|---|---|
| `climate` | `switch` or `power`, `temp_current`, `temp_set`, `heating_state`, `mode` |
| `sensor` | `open`, `battery` |
| `vacuum` | `state`, `command`, `fan_speed`, `error_code`, `battery`; a generic card without `state` or `command` |
| `cover` | `control`, `percent_control`, `percent_state`, `battery`; a generic card without any of the first three |
| `light`, `plug`, `switch`, `air`, `fan`, `safety`, `lock`, `appliance`, `infrastructure` | Generic card, grouped by type |

A device that can't be read for a reason other than its power or Wi-Fi, like a Bluetooth device on a LAN integration, can say so: set `online` to false and add a read-only `String` value with the code `unavailable`. Its card shows that text instead of the offline message, and it isn't counted as offline.

Using these codes also makes your devices work with shared scripts: the heating example reads `heating_state` from
thermostats and `open` from window sensors, whoever made them.

## Device ids

Scripts and the page see a device as `<extension id>:<device id>`, such as `tuya:<id>`, `xiaomi:<did>`, `gree:<mac>` or
`hue:1`. The extension supplies only the second part, and it must be stable across restarts and unique within the
extension. `rooms` in `config.json` and `home.device("...")` use the full id.

## A service in any language

For USB, Bluetooth, serial ports, native libraries or an existing program in another language, run a small helper service
and point [`extensions/service-bridge`](../extensions/service-bridge/README.md) at it. The helper serves two endpoints:

```
GET  /devices                 returns a JSON array of devices
POST /devices/{id}/dps/{dp}   body {"value": ...}; any 2xx means accepted
GET  /data                    optional: anything JSON can hold, readable as home.plugin("service-bridge").data()
```

Then add the extension to `config.json`:

```json
{ "source": "./extensions/service-bridge", "settings": { "host": "garage-helper:9000", "token": "${GARAGE_TOKEN}" } }
```

- The device ids are prefixed with the extension's id, so `lamp` becomes `service-bridge:lamp`. To run a second helper,
  copy the folder and change its `id`.
- `token` is optional and is sent as `Authorization: Bearer <token>`, only to `host`. Check it in the helper if anything
  else can reach it.
- Writes are validated against what you declared before they reach you, but validate them too.
- If the helper stops answering, its devices show as offline.
- `host` is `host` or `host:port` over plain HTTP, not a URL.

[`examples/adapters/virtual-lamp`](../examples/adapters/virtual-lamp/main.go) is a helper for it in about 50 lines of Go,
and [`examples/services`](../examples/services/README.md) has one in Python. Run it with
`go run ./examples/adapters/virtual-lamp`.

## Getting people's devices in

How credentials get obtained is up to each extension. The existing ones read a file you put in your config folder's
`secrets/`, written by a vendor-specific tool, through a `file` setting (see [Settings](extension-api.md#settings)).
Document your steps in your extension's README, and read secrets from a `file` or `secret` setting, never from the repo.
