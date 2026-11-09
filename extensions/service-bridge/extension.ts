// A bridge to a helper service you run yourself, in any language, for what a sandbox can't do: USB, Bluetooth, serial
// ports, native libraries. The helper speaks a small HTTP protocol (docs/devices.md):
//   GET  /devices                 a JSON array of devices
//   POST /devices/{id}/dps/{dp}   body {"value": ...}; any 2xx means accepted
//   GET  /data                    optional: anything JSON can hold, for scripts to read as plugin data
const AUTH_FREE = "";
const WRITABLE = ["Boolean", "Enum", "Integer", "Action"];

// A value's code is what scripts see, and its dp is how the helper addresses it, so remember which is which.
const dpOf: Record<string, Record<string, string>> = {};

// What the helper functions below need from ctx, so they don't depend on the inferred settings type.
type Ctx = { fetch: ExtensionContext<{}>["fetch"]; settings: { host: string; token?: string; data: boolean } };

function call(ctx: Ctx, method: string, path: string, body?: unknown): FetchResponse {
  const headers: Record<string, string> = { "User-Agent": "home-control-service-bridge" };
  if (ctx.settings.token) headers.Authorization = `Bearer ${ctx.settings.token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = ctx.fetch(`http://${ctx.settings.host}${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (response.status === 401 || response.status === 403) throw new Error(`the helper refused the token (${response.status})`);
  if (!response.ok) throw new Error(`the helper answered ${response.status} to ${method} ${path}`);
  return response;
}

defineExtension({
  id: "service-bridge",
  name: "Service bridge",
  version: "1.0.1",
  license: "MIT",
  description: "Devices and data from a helper service you run yourself, in any language.",
  categories: ["bridges"],
  tags: ["bridge", "helper service", "custom devices"],
  access: { network: ["{host}"] },
  settings: {
    host: { label: "Helper address", type: "string", required: true, help: "Where the helper listens, as host or host:port, for example 192.168.1.20:9000. It is contacted over plain HTTP." },
    token: { label: "Helper token", type: "secret", sendTo: ["{host}"], help: "Sent to the helper as a bearer token. Check it in the helper if anything else can reach it." },
    data: { label: "Read /data", type: "boolean", default: false, help: "Also fetch the helper's GET /data for scripts to read with home.plugin(\"service-bridge\").data()." },
  },
  data: {
    everySeconds: 60,
    fetch(ctx) {
      // Rather no data than a made-up value: scripts see null until the owner turns this on.
      return ctx.settings.data ? call(ctx, "GET", "/data").json() : null;
    },
  },
  devices: {
    everySeconds: 10,
    poll(ctx) {
      const remote = call(ctx, "GET", "/devices").json();
      if (!Array.isArray(remote)) throw new Error("the helper's /devices must be a JSON array of devices");

      return remote.map((d: any): ExtensionDevice => {
        if (typeof d?.id !== "string" || d.id === "" || typeof d.name !== "string" || !Array.isArray(d.dps)) {
          throw new Error("the helper sent a device without an id, a name or dps");
        }
        const codes: Record<string, string> = {};
        const values = d.dps.map((dp: any): DeviceValue => {
          const code = dp.code || dp.dp;
          if (typeof code !== "string" || code === "" || typeof dp.dp !== "string") throw new Error(`device ${d.id} has a data point without a dp or code`);
          codes[code] = dp.dp;
          const limits = dp.values ?? {};
          return {
            code,
            type: dp.type,
            writable: dp.writable === true && WRITABLE.includes(dp.type),
            value: dp.value ?? null,
            range: limits.range,
            min: limits.min,
            max: limits.max,
            step: limits.step,
            unit: limits.unit,
            scale: dp.scale || undefined,
          };
        });
        dpOf[d.id] = codes;
        return { id: d.id, name: d.name, kind: d.kind || undefined, product: d.product || undefined, online: d.online !== false, values };
      });
    },
    write(ctx, deviceId, code, value) {
      const dp = dpOf[deviceId]?.[code];
      if (dp === undefined) throw new Error(`the helper has no ${code} on ${deviceId}`);
      call(ctx, "POST", `/devices/${encodeURIComponent(deviceId)}/dps/${encodeURIComponent(dp)}`, { value });
    },
  },
});
