# Devices

An integration connects a family of devices. Everything above it, the API, the page and automations, works only with the
device model below, so a new integration gets all of them for free.

Every integration is an [extension](extensions.md). For a LAN protocol over TCP or UDP, start from
[tuya](https://github.com/drax98/tomtly-extensions/tree/main/tuya), [xiaomi](https://github.com/drax98/tomtly-extensions/tree/main/xiaomi) or
[gree](https://github.com/drax98/tomtly-extensions/tree/main/gree).

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

Scripts and the page see a device as `<extension id>:<device id>`, such as `tuya:<id>`, `xiaomi:<did>`, or `gree:<mac>`. The extension supplies only the second part, and it must be stable across restarts and unique within the
extension. `rooms` in `config.json` and `home.device("...")` use the full id.

## Getting people's devices in

How credentials get obtained is up to each extension. Tuya and Xiaomi take the file a vendor-specific tool writes, chosen
on the page through a `file` setting (see [Settings](extension-api.md#settings)), and keep it encrypted in the database.
Document your steps in your extension's README, and read secrets from a `file` or `secret` setting, never from the repo.
