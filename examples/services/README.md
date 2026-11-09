# Helper services

A helper is a small program, in any language, that does what the sandbox can't (Bluetooth, USB, serial, a native
library) and exposes it as the HTTP protocol in [docs/devices.md](../../docs/devices.md#a-service-in-any-language). Point the
[service-bridge](../../extensions/service-bridge/README.md) extension at it.

Below is a Python helper for a Bluetooth-style thermometer using only the standard library.

> **Untested on real hardware.** `read_temperature()` is a stub that returns a fixed number. The protocol part is
> checked only by the fake-server tests of the bridge, and this file has never been run against a Bluetooth or USB
> device. Replace the stub with, for example, a `bleak` read, and expect to debug it.

```python
#!/usr/bin/env python3
"""python3 helper.py   (listens on 0.0.0.0:9000; set TOKEN to require a bearer token)"""
import json, os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOKEN = os.environ.get("TOKEN", "")
buzzer = False


def read_temperature():
    return 215  # STUB: read your Bluetooth/USB sensor here. 215 with scale 1 shows as 21.5


class Handler(BaseHTTPRequestHandler):
    def reply(self, status, body=None):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def authorised(self):
        if TOKEN and self.headers.get("Authorization") != "Bearer " + TOKEN:
            self.reply(401)
            return False
        return True

    def do_GET(self):
        if not self.authorised():
            return
        if self.path == "/devices":
            try:
                temp, online = read_temperature(), True
            except Exception:  # the sensor is out of range: say so, don't invent a reading
                temp, online = None, False
            self.reply(200, [{
                "id": "ble-thermometer", "name": "Bluetooth thermometer", "kind": "sensor", "online": online,
                "dps": [
                    {"dp": "t", "code": "temp_current", "type": "Integer", "values": {"unit": "°C"}, "scale": 1, "writable": False, "value": temp},
                    {"dp": "beep", "code": "buzzer", "type": "Boolean", "values": {}, "writable": True, "value": buzzer},
                ],
            }])
        elif self.path == "/data":
            self.reply(200, {"note": "anything JSON can hold"})
        else:
            self.reply(404)

    def do_POST(self):
        global buzzer
        if not self.authorised():
            return
        parts = self.path.strip("/").split("/")  # devices/{id}/dps/{dp}
        if parts[:1] == ["devices"] and len(parts) == 4 and parts[1] == "ble-thermometer" and parts[3] == "beep":
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
            if isinstance(body.get("value"), bool):
                buzzer = body["value"]  # STUB: send it to the device here
                return self.reply(202)
        self.reply(400)


ThreadingHTTPServer(("0.0.0.0", 9000), Handler).serve_forever()
```

Return `null` for what you can't read and set `online` to false when a device is out of range, rather than guessing:
scripts treat unknown differently from off. Answer a non-2xx status when a write fails, so the page shows it. If anything
besides home-control can reach the port, set `TOKEN`.
