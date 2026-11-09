# Setting up the bundled integrations

Each bundled integration is an extension in `extensions/`. Copy the ones you use into your config folder's `extensions/`
and list them under `extensions` in `config.json`, as below. Each `extensions/<name>/README.md` says what it was tested
against.

## Tuya

```json
{ "source": "./extensions/tuya", "settings": { "devices": "secrets/tuya.json", "hosts": "<gateway id>=192.168.1.10@3.4,<wifi device id>=192.168.1.11@3.5" } }
```

`hosts` lists what's reached directly: a gateway carries its Zigbee devices, and each Wi-Fi device is listed on its own. `devices` is a path inside your config folder to the file the tinytuya wizard writes, which holds every device's local key. Fetch it again after re-pairing a device, since re-pairing changes the key.

```bash
mkdir -p ~/tuya-lan && docker run --rm -it -v ~/tuya-lan:/app -w /app python:3.12-slim \
  sh -c "pip install -q tinytuya && python -m tinytuya wizard"
cp ~/tuya-lan/devices.json ~/.config/home-control/secrets/tuya.json
```

## Xiaomi

```json
{ "source": "./extensions/xiaomi", "settings": { "devices": "secrets/xiaomi.json" } }
```

`devices` comes from [Xiaomi-cloud-tokens-extractor](https://github.com/PiotrMachowski/Xiaomi-cloud-tokens-extractor) and holds every device's token and IP. Choose `q` at the prompt, then open `http://127.0.0.1:31415` and scan the QR code with the Mi Home app:

```bash
docker run --rm -it -p 127.0.0.1:31415:31415 -v ~/.config/home-control/secrets:/out python:3.11 sh -c '
  cd /tmp &&
  wget -q https://raw.githubusercontent.com/PiotrMachowski/Xiaomi-cloud-tokens-extractor/c4db715dace9806e905153c2977608873e8ab7c9/token_extractor.py &&
  pip install -q requests pycryptodome charset-normalizer Pillow colorama &&
  python token_extractor.py -o /out/xiaomi.json'
```

Fetch it again after re-pairing a device. If a device's IP changes it is found again on its own, see [When a device's address changes](#when-a-devices-address-changes). Any other model is driven from its description at [miot-spec.org](https://miot-spec.org), which it asks for once, so the service tells that public server which models you own. See [its README](../extensions/xiaomi/README.md) for what's covered and what isn't.

## Gree

```json
{ "source": "./extensions/gree", "settings": { "host": "192.168.1.12", "name": "Living room AC", "mac": "aabbccddeeff" } }
```

No credentials are needed. Use the AC's IP address, because broadcast discovery doesn't work from Docker Desktop. `mac` is the AC's MAC address, without separators or with `:`. It's optional, but it lets the AC be found again after a DHCP change without guessing. For a second AC, copy `extensions/gree` and change its `id`.

## Hue, a helper service, JSON

```json
{ "source": "./extensions/hue", "settings": { "host": "192.168.1.13", "apiKey": "${HUE_API_KEY}" } },
{ "source": "./extensions/service-bridge", "settings": { "host": "192.168.1.20:9000", "token": "${BRIDGE_TOKEN}" } },
{ "source": "./extensions/http-json", "settings": { "host": "192.168.1.21:9000", "path": "/today" } }
```

## When a device's address changes

Tuya hosts, Xiaomi IPs and the Gree address come from your configuration. When a device has been unreachable for a minute, its extension probes the other addresses in the same /24 network as its last private address (for example `192.168.1.1` to `.254`, private address ranges only), at most once every 5 minutes per device, all at once with `ctx.udp.scan` or `ctx.tcp.scan`. It moves to a new address only if the device there proves it is the same one, and logs a warning with the old and new address. The device keeps its id, so scripts and the page don't notice.

- Tuya: TCP port 6668, accepted only if the device proves it holds its local key: the encrypted handshake on 3.4 and 3.5, or a status read with a reply that decrypts on 3.2 and 3.3. Nothing else is sent. Addresses other Tuya links use are not probed, and a device on 3.1 can't prove itself, so it isn't looked for.
- Xiaomi: UDP port 54321, accepted only if the device id matches the file's `did` and a read authenticated with its token works. Devices whose `did` isn't a plain number are not searched for.
- Gree: UDP port 7000, accepted if the MAC matches the one learned at the first connection or set as `mac`. If the MAC was never known (the service restarted while the address was already stale), the only unit that answers is assumed to be the AC and a warning says so; if several answer, nothing moves. Setting `mac` avoids the guess.

The new address lives in memory only: config files are never rewritten (the config is mounted read-only). After a restart the old address is tried first and the search repeats after a minute. Another subnet, or a network larger than /24, is not searched. These extensions ask for the `lan` access to do it, which you approve like any other.

Reserving IPs on your router is still the best fix. If a device is re-paired, its key or token changes, so fetch the credentials file again; if its id changes too, scripts naming the old id show an error until you update them.
