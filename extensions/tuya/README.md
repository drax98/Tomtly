# Smart Life (Tuya)

Tuya Wi-Fi, Zigbee and Bluetooth mesh devices paired in Smart Life or Tuya Smart, over the local network, written only on
`ctx.tcp` and `ctx.crypto`. Protocols 3.1 to 3.5. One connection per entry in `hosts`: a gateway with its children, or a
Wi-Fi device. Device ids are `tuya:<Tuya id>`.

## Status

What has been run against real hardware is one home: one Zigbee gateway with its children, reading only. That was before
this version, so 1.1.0, with its new rules and modules, has yet to run there. **Everything else below has only been tested
against fakes written from the documentation, in `internal/extension/tuya_*_test.go`, and no one has yet run it on a real
device of that kind.**

| | Real hardware (1.0) | Fakes only |
|---|---|---|
| Reads | The protocol that gateway uses (3.4 or 3.5), and the categories in the `wk dd cz cl pc tdq wg2 mcs cobj ywbj` rules, which were tuned on it | Every other category; protocols 3.1, 3.2, 3.3 and the other of 3.4 and 3.5; a second gateway; Bluetooth mesh children, which work the same way on paper |
| Commands | None | All of them, on every protocol and category |
| Finding a moved device | None | All of it |

A category being listed means it gets a sensible card and safe write rules, not that its values were checked on a device.

## Protocols

| Version | Status reads | Commands | Proof of who answered |
|---|---|---|---|
| 3.1 | Plain text | AES-ECB, base64, MD5 part | None possible. Reads work with any key and prove nothing, so a device on 3.1 is never looked for at another address |
| 3.2 | As 3.3, always the `device22` way | AES-ECB | A reply that decrypts |
| 3.3 | AES-ECB | AES-ECB | A reply that decrypts |
| 3.4 | AES-ECB, HMAC | AES-ECB, HMAC | Session key handshake |
| 3.5 | AES-GCM | AES-GCM | Session key handshake |

`device22` is firmware that answers a normal status read with `data unvalid`. The extension switches when it gets that
answer, and lists the mapping's data points as nulls from then on. It doesn't go by the id being 22 characters, since many
ordinary devices have such ids. The protocols follow [TinyTuya](https://github.com/jasonacox/tinytuya), see `NOTICE`.

Links on 3.2 and 3.3 have no handshake, so a device shows offline until something it sent decrypts with its key, and
nothing can be written to it before that. On 3.1 reads work without the key, so a wrong key only shows when a command fails. A link with nothing readable for 30 seconds restarts, with a hint about the key
or version in the log.

## Categories

`categories.ts` lists 141 categories with their source: Tuya's 103, 37 more from Home Assistant's `tuya` integration, and
one from Tuya's lighting docs. A category it doesn't list shows as `other`, and any `wg*` as a gateway.

| Kind | Categories |
|---|---|
| `light` | `dj xdd fwd dc dd gyd tyndj tgq tgkg sxd dsd hcdd hxd mbd qjdcz tyd` |
| `switch` | `kg cjkg ckqdkg szjqr znjxs` |
| `plug` | `cz aqcz` |
| `cover` | `cl clkg ckmkzq mc jdcljqr` |
| `climate` | `wk wkf wkcz kt ktkzq ntq qn bgl rs yb dbl dr znrb` |
| `air` | `kj xfj jsq cs xxj cwjwq` |
| `fan` | `fs ks fsd fskg` |
| `vacuum` | `sd` |
| `sensor` | `mcs pir hps zd wsdcg qxj ldcg ylcg pm2.5 voc hjjcy szjcy ywcgq zwjcy znsb wxkg` |
| `safety` | `mal sgbj sos ywbj cobj co2bj rqbj jwbj jqbj dgnbj sj sp dghsxj` |
| `lock` | `ms jtmspro jtmsbh gyms hotelms ms_category videolock photolock mk bxx` |
| `appliance` | `bx xy yg lyj mjj sf sjz ds tyy js bh tnq mb kfj nnq cn mzj mg dcl kqzg znfh swtz cwwsq cwtswsq cwwqfsq cwysj msp ggq sfkzq sz hwsb amy liliao ts tzc1 sb tracker znyh bzyd` |
| `infrastructure` | `pc tdq dlq znjdq zndb qccdz xnyjcn znnbq wg2 ykq wnykq` |

`pc` and `tdq` are in `infrastructure` because that's where the first home keeps them, and it mustn't change.

### What can be written

The cloud mapping has no read/write flag, so `rules.ts` goes by what Tuya documents the codes to mean:

- **Never:** factory reset, restart, consumable and map resets, pairing, firmware, Wi-Fi, formatting and unlock codes, in any category.
- **Report-only categories never write anything:** sensors, detectors, meters, locks, safes and cameras. A wrong guess must not open a door.
- **Other listed categories** write Boolean, Enum and Integer values, apart from codes named as readings (`cur_*`, `*_state`, `*_current` and so on).
- **A category nobody listed** writes only what's plainly a command, like `switch_1` or `bright_value`.
- **`wk dd cz cl pc tdq wg2`** keep the rule the first home was tuned on, so a real thermostat there can offer a value like `valve_state` that a device ignores.

## Values

- Scale comes from the mapping. The exceptions read as tenths, where its limits show it's wrong: `temp_current` on climate devices with a maximum of 200 or more, and `cur_voltage` with a maximum of 1000 or more. The categories tuned on the first home always read those two as tenths, so a `wk` or `cz` that really reports whole numbers shows a tenth of its value.
- `Json`, `String` and `Raw` are read-only text, because the service only writes Boolean, Enum, Integer and Action. The page shows JSON as pairs and cuts long text short. `Bitmap` shows the names of its bits, or the number if a bit has no name.
- **Colour:** `colour_data` reads as words like `hue 220, saturation 100%, brightness 50%`, and `colour_hue`, `colour_saturation` and `colour_brightness` are writable numbers. A write goes out in the form the light last sent (JSON or hex) and turns on colour mode. They can't be written until the light has reported a colour, because its form isn't known before that.
- **Battery sensors** have no value until they report, and show offline until then, including after this service restarts. One that reported before comes back when its gateway does. Values are never invented.

## Not supported

- **Commands through `Json`, `String` or `Raw`:** scene and music data, IR codes (`wnykq`, `ir_send`), lock commands. The service has no writable text value. Scenes built into a light can be chosen with `work_mode`.
- **More than 15 direct connections:** the sandbox allows 16 open, and one is kept for finding moved devices. Further entries in `hosts` show as offline. Zigbee and mesh children don't count, so put them behind a gateway.
- **Battery Wi-Fi sensors** sleep with their radio off, so a direct link can't reach them, and they would use a connection. Leave them out of `hosts`.
- **Bluetooth devices with no gateway** have no local connection at all.
- **Cameras** only show what their data points report. Video isn't on this protocol.
- **Finding devices automatically:** `hosts` is written by hand. Tuya's UDP broadcasts on 6666 and 6667 would need those ports published from Docker and a new `access.listen`, for what `python -m tinytuya scan` already tells you.

## Access

- `lan`: device addresses come from your list, and a device that moved is looked for on its /24.
- `sockets`: the protocol is TCP on port 6668.
- It also reads the file you choose for **Devices file**.

When a link has been failing for a minute, and at most every 5 minutes, it tries the other addresses of the /24 of the
last private address, and adopts one only if that device proves it holds the key. On 3.4 and 3.5 that's the encrypted
handshake, and nothing else is sent. On 3.2 and 3.3 it's one status read, which changes nothing, with a reply that has to
decrypt. ECB has no nonce, so a recorded reply could be replayed, though what it then gets written can't be read without the key.

## Settings

| | |
|---|---|
| `devices` | Path inside your config folder to what `python -m tinytuya wizard` writes, such as `secrets/tuya.json`. It holds every local key, and each `key` is refused in any output or request. |
| `hosts` | `<device id>=<ip>@<version>`, separated by commas, with a version from 3.1 to 3.5. The ip and version may be left out if the file has them, as the wizard's file does when its scan found the device. |

`python -m tinytuya scan` lists the id, address and version of what's on your network.

```json
{ "source": "./extensions/tuya", "settings": { "devices": "secrets/tuya.json", "hosts": "<gateway id>=192.168.1.20@3.4,<plug id>=192.168.1.21@3.3,<other id>" } }
```

**Gateways:** a sub-device (one with a `node_id`) goes to the gateway its `parent` names. With no `parent`, it goes to the
gateway with the same local key, which is how the wizard itself guesses. Failing that it goes to the first gateway in
`hosts`, so a home with two gateways and neither clue in its file should fetch the file again with a newer wizard.

## Notes

- A value's `code` comes from the cloud mapping, with known mistakes corrected. A repeated code gets `_<data point>` added, and a missing one is `dp_<data point>`.
- A command sent while the link is down throws, rather than waiting for it to come back.
