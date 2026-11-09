# Xiaomi Home

Xiaomi and Roborock devices over miIO, the encrypted UDP protocol they speak on port 54321, written only on `ctx.udp` and
`ctx.crypto`. **It only uses your local network. It never calls the Xiaomi cloud**, so a device with no local protocol can't
be read. The one exception to staying local: the first time it sees a model, it asks Xiaomi's public spec server what that
model offers, see [Access](#access).

## How a device is driven

The first of these that fits:

1. **A hand-written table** in `models.ts`: `zhimi.airp.mb5`, `zhimi.humidifier.ca6`, `dmaker.fan.1c`, `deerma.humidifier.jsq5`, `yeelink.light.ceiling5`. Tuned on real hardware, and frozen by `TestXiaomiKnownModelsKeepTheirTables`, since scripts may use their names and limits.
2. **Roborock's own commands**, for `roborock.vacuum.*` and `rockrobo.vacuum.*`. They don't take MIoT calls, and a real S5 was seen refusing them. Only the S5 can set its fan speed, since other generations number it differently: on the others it's only shown. Newer Roborocks may not answer miIO at all, and then show as offline.
3. **The model's MIoT spec**, when the file's `spec_type` names one: anything else with a spec. See `spec.ts`.
4. **An older model's commands**, in `legacy.ts`, when there's no `spec_type`, or the device refuses MIoT calls.
5. **Nothing**, and the card says why.

## Status

What ran on real hardware is one home's 4 models that were online, **reading only**. No command has ever been sent to a real device by this integration.

| | Real hardware | Fakes only (`internal/extension/xiaomi_*_test.go`) |
|---|---|---|
| Hand tables | Reads on `zhimi.airp.mb5`, `zhimi.humidifier.ca6`, `dmaker.fan.1c`, `roborock.vacuum.s5` | `deerma.humidifier.jsq5` and `yeelink.light.ceiling5` weren't answering when checked; every command |
| Spec driver | Reads on `zhimi.airp.mb5`, `zhimi.humidifier.ca6` and `dmaker.fan.1c` with their tables bypassed, in a throwaway copy: values matched the tables. An S5 refusing MIoT, which then says so | Every other model. Writes, actions, colours, hiding properties, batch sizes, the cache |
| Older families | None: nobody here has one | All of them |
| Finding a moved device | None | All of it |
| Budgets | None: one home has too few devices to reach them | All of it |

A device type being listed means it gets a sensible card and safe rules, not that a device of that type was checked.

## From a spec

Each service's properties and actions become values, named from the spec, so `target-time` in the main service is `target_time` and in another is `<service>_target_time`. Common ones get the names the page and shared scripts use: `power`, `temp_current`, `temp_set`, `humidity`, `battery`, `pm25`, `child_lock`, `filter_life`, and for a vacuum or a curtain what their cards read. Several switch services are `switch_1`, `switch_2` and so on.

| Spec | Becomes |
|---|---|
| `bool` | `Boolean` |
| An integer with `value-list` | `Enum`, named from the list |
| An integer with `value-range` | `Integer` with its limits if writable, a reading if not |
| An integer with no range | A reading, even if the spec says write, since nothing says what it may hold |
| A writable `float` | `Integer` with a scale from its step, so 20.5 is 205 at scale 1 and writes back as exactly 20.5. Finer than 0.001, or with no range, it's a read-only `Number` |
| A read-only `float` | `Number`, to 4 decimals |
| `string` | Read-only `String`, cut at 200 characters. The service has no writable text |
| `uint32` colour | `#rrggbb`, with `<code>_hue` and `<code>_saturation` writable beside it |
| An action with no arguments | A choice in `command` |

### What stays out

Writable only if the spec's access says write, and a value the spec can't read or write isn't shown. On top of that:

- **Never shown**, by a service's, property's or action's name or description: device information, firmware and upgrades, reboot and restart, factory anything, reset and restore, debug, Wi-Fi and network, country and region, cloud and server settings, pairing and binding, adding or deleting remotes, serial numbers, MAC and IP addresses, passwords and tokens, location, RFID, calibration and motor learning, flash writing, privacy consent, and clearing or deleting data. `spec.ts` has the exact words.
- **Filter and consumable resets need a person to decide, so they're hidden**: `reset-filter-life`, `reset-brush-life`, `reset-dust-bag-life`, `reset-mop-life`, `reset-desiccant-life`, `reset-deodorant-life`, `reset-sensor-clean-alert-life`, `reset-sterilizer-module-life` and any other `reset-*`, as well as `restore-map` and `restore-default-settings`. A consumable's level is read-only, even where the spec lets it be written.
- **A choice in an `Enum` with an unsafe name** can be reported, but not asked for.
- **Only report, whatever their spec says:** every sensor, lock, door and safe, cameras, doorbells, intercoms, meters and trackers. A wrong guess must not open a door. Anything unlocking, disarming or arming is read-only on any device too.
- **Actions with arguments** can't be sent, since the service only carries a choice from a list. On 524 sampled specs that's about a third of all actions: `set-menu`, `set-recipe`, and so on.

A property the device says it doesn't have (`-4001` and its kin) leaves the card after three reads. Other codes leave it unknown. Past 64 values a device's list stops, main service first, which 7 of 524 sampled specs reach. `xiaomi_spec_test.go` covers all of this.

These rules can't know what a vendor's own property does. A property with a harmless name in a vendor's service is writable if its spec says so, and so is starting a kettle, cooker or heater with nobody home.

## Device types

`kinds.ts` lists all 200 real device types from the [official list](https://miot-spec.org/miot-spec-v2/instances?status=all) as of 2026-11-01. Its other two, `other` and `test-xjpl`, and any newer type show as `other`, with a generic card. A long list on a generic card shows 8 values, the writable first, and the rest under **Advanced settings**.

## Older models

| Family | Models | Can change |
|---|---|---|
| Yeelight | `yeelink.light.*` and `yeelink.bhf_light.*` with no spec, or refusing MIoT | Power, brightness, colour temperature |
| Philips | `philips.light.bulb`, `cbulb`, `candle`, `candle2`, `downlight`, `ceiling`, `zyceiling` | Power, brightness, colour temperature |
| Purifiers | `zhimi.airpurifier.v1`-`v7`, `m1`, `m2`, `ma1`, `ma2`, `sa1`, `sa2`, `mc1`, `mc2` | Power, mode, favourite level, sound, child lock, display |
| Humidifiers | `zhimi.humidifier.v1`, `ca1`, `cb1`, `cb2` | Power, mode, target, sound, child lock, drying |
| Plugs | `chuangmi.plug.v1`, `v3`, `m1`, `m3`, `v2`, `hmi205`, `hmi206`, `hmi208` | Power, USB on `v1` and `v3` |
| Fans | `zhimi.fan.v2`, `v3`, `sa1`, `za1`, `za3`, `za4` | Power, speed, natural speed, swing, child lock |
| Aqara gateway | `lumi.gateway.v3`, `mieu01`, `mgl03`, `aqhm01`, `lumi.acpartner.v1`-`v3` | Nothing: it shows arming and its light's colour |

The tables are written from the protocol as [python-miio](https://github.com/rytilahti/python-miio)'s source shows it. It's GPL-3.0 and none of its code is used. No family has run on real hardware. A mode or range can differ within a family, and the device then refuses that choice. `reset_filter1` and `set_arming` are left out on purpose.

## Not supported, and why

Each has a card that says so, unless noted:

- **Bluetooth and Mesh devices** (`did` starting `blt.`): no local network connection at all.
- **Zigbee sub-devices** (`did` starting `lumi.`) and anything else whose `did` isn't a plain number: they answer through their gateway or the cloud. Reading a gateway's children takes a separate set of calls that differ by gateway, and it isn't done.
- **Any device with no local address or token in the file**, which is usual for a device behind a gateway, or an address that isn't private.
- **A model with no `spec_type` and no family here**: older ones like `chunmi.cooker`, `viomi`, `nwt.derh`, `cgllc.airmonitor`, `deerma.humidifier.mjjsq` and `dmaker.airfresh`. Most newer models of these have a spec, and these need someone with the device to check them. A file from an old extractor may leave out `spec_type`, so fetch it again.
- **A spec that lists nothing but device information**, which 95 of 524 sampled specs do. Most are Bluetooth devices anyway.
- **A device that doesn't answer on the LAN even with a token**, which some cloud-only devices do. It shows as offline, the same as one with no power.
- **Cameras' video and speakers' audio:** not on this protocol. Android TV boxes aren't miIO, so they stay offline.
- **Commands to Zigbee or Bluetooth through the cloud, or any cloud read:** on purpose. This integration is LAN only.

## Access

- `lan`: any private address, because each device's address comes from your devices file, and to look for a device that moved.
- `sockets`: the protocol is UDP.
- `network: miot-spec.org`: the description of a model it has no table for, once per model and version. **That request names the model, so Xiaomi's public spec server can see which models you own, from your address.** Tokens, device ids, names and addresses never go there. Models with a table make no request. If your home has only those, nothing is sent at all.
- `storage`: keeps the part of each description it needs, so a restart with no internet still knows the model. It stays under 900 KiB of the 1 MiB allowed, dropping the oldest model first, which is then asked for again after a restart.

Why not a list bundled with the extension? About 15,000 released models at a median 3.5 KB is far over a package's 4 MiB, and it would go stale. And it doesn't read the site's list of all models, which at 6.9 MB is over the 4 MiB a fetch may return: the devices file's `spec_type` already says which one to ask for.

Upgrading to this version asks for your approval once, because it asks for more access.

With no internet, a model already saved works as usual, and a new one shows as waiting and is tried again, after 1 minute and then up to every hour. A model the server doesn't know is asked for again every 6 hours. Nothing is guessed in the meantime, and the rest of the list carries on.

## Settings

| | |
|---|---|
| `devices` | A path inside your config folder, like `secrets/xiaomi.json`: the output of [Xiaomi-cloud-tokens-extractor](https://github.com/PiotrMachowski/Xiaomi-cloud-tokens-extractor). It holds every device's token. |

```json
{ "source": "./extensions/xiaomi", "settings": { "devices": "secrets/xiaomi.json" } }
```

Scripts see a device as `xiaomi:<did>`, so its id survives an address change.

## Notes

- Devices are polled every 10 seconds, one after another. One that doesn't answer is retried every 30 seconds, so it doesn't delay the others. Its last readings stay while it is offline.
- Polling stays under 400 of the 600 operations a minute the service allows, with a share of about 67 in each poll, and each call is two. Past that a big home is read in turns, so each device is read less often and none is left out. About 30 single-call devices fit in every poll.
- One device's turn is at most 8 seconds and one poll's share, and a very long list goes on from there next time.
- A device first gets one property, which proves the token and that it speaks MIoT, and then up to 15 in a call. If it stops answering long lists, or refuses them, the calls get shorter until it does. That size is only kept in memory.
- A device that fails for a minute is looked for on the /24 of its last private address, at most every 5 minutes. A new address is adopted only when the id there matches and a read that needs the token succeeds. Nothing is written. The new address is kept in memory, so after a restart the old one is tried first.
- A command is sent again once if its reply is lost, as UDP can lose a packet. A refusal is never repeated, and an action never is.
- The list of devices may not pass 1 MiB. It takes over 100 devices with the longest specs to get near, and past 900 KiB the longest give up their values and say so.
- Tokens never leave as they are: the service refuses any payload holding one, and any value returning one.

## Licence

MIT, like the rest of the repository.
