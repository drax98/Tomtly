# Gree air conditioner

Gree-based air conditioners (Gree, Sinclair, EWPE Smart) over the local network, written only on `ctx.udp` and
`ctx.crypto`. It is the proof that a hard binary protocol fits in the sandbox: UDP port 7000, an AES handshake (scan, then
bind with the published generic key to get the unit's own key), status reads and command writes, in both the ECB and
the GCM scheme. It uses the `climate` kind and the codes the page's climate card understands (`power`, `mode`,
`temp_set`, `fan_speed`, `quiet`, `turbo`, `light`, `temp_current`), so shared scripts work alike. The device id is `gree:<mac>`.
`fan_speed` is a name (`auto`, `low`, ... `high`). The unit's other columns appear when it reports them: `swing_vertical`,
`swing_horizontal`, `fresh_air`, `x_fan`, `health`, `sleep`, `frost_protection` and `energy_saving`. Units often answer 0 for a
feature they don't have, so one of these showing doesn't prove the unit has it.

## Status

- **Reads: tested on a real unit**, read-only (scan, bind and status). It returned all eight values, with no scan
  warnings and no violations. The unit used one scheme only, so the other is covered by the fake below. Model and
  firmware are not recorded.
- **Writes: tested only against a test fake** in `internal/extension/gree_ext_test.go`, in both schemes. The bytes decrypt
  to the intended command. **Never sent to a real air conditioner.**
- Rediscovery is tested against the fake only.
- Only one unit per extension. For a second unit, copy the folder and change its `id`.

## Access

- `network: ["{host}"]`: the unit's own address, from your setting.
- `sockets`: the protocol is UDP.
- `lan`: only to look for the unit on its /24 after it has been unreachable for a minute, see below. There is no broadcast
  discovery, so you give the address.

## Settings

| | |
|---|---|
| `host` | The unit's IP address, without a port. Reserve it on your router. Find it in your router's client list. |
| `mac` | Optional, like `aabbccddeeff`. The unit's MAC address, so it can be found again after an address change without guessing. |
| `name` | Shown on the card. "Air conditioner" by default. |

```json
{ "source": "./extensions/gree", "settings": { "host": "192.168.1.12" } }
```

The device id is `gree:<mac>`, so it survives an address change.

## When the address changes

After a minute of failures, and at most every 5 minutes, it sends the scan message to the other addresses of the /24 of
the last private address, at once with `ctx.udp.scan`, and moves to the unit whose MAC matches `mac` (or the one it learned
at its first connection). Without any known MAC, a single unit answering is assumed to be this one and a warning says so;
several answering means nothing moves. Set `mac` to avoid the guess. The new address is kept in memory only.

## Notes

- Bind does not change the unit's state, but the unit may show it in the vendor app's pairing state. Binding happens on
  the first poll, and again when a poll fails.
- A write sends one command, once. If a datagram is lost you see the error, and the next poll binds again.
- Setting the temperature also sends `TemUn=0`, forcing Celsius.

## Licence

MIT, like the rest of the repository.
