import { describe, expect, it } from "vitest";
import { loadExtension, makeHost } from "./harness";

const load = () => loadExtension(() => import("../service-bridge/extension.ts"));
const base = "http://192.168.1.20:9000";
const headers = { "User-Agent": "home-control-service-bridge" };

const devices = [
  {
    id: "dev1",
    name: "Heater",
    kind: "climate",
    product: "Panel",
    dps: [
      { dp: "1", code: "power", type: "Boolean", writable: true, value: true },
      { dp: "2", type: "Integer", writable: true, value: 21, scale: 1, values: { min: 5, max: 30, step: 1, unit: "C" } },
      { dp: "3", code: "mode", type: "Enum", writable: true, value: "auto", values: { range: ["auto", "heat"] } },
      { dp: "4", code: "label", type: "String", writable: true, value: "kitchen" },
      { dp: "5", code: "fault", type: "Boolean", writable: false, value: null },
    ],
  },
  { id: "a b/c", name: "Sensor", online: false, dps: [{ dp: "t", code: "temp", type: "Number", value: 3 }] },
];

describe("service-bridge", () => {
  it("maps the helper's devices to the shape the page expects", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "192.168.1.20:9000" }, fetch: () => ({ body: devices }) });
    const [heater, sensor] = def.devices.poll(host.ctx);

    expect(host.fetches).toEqual([{ url: `${base}/devices`, options: { method: "GET", headers, body: undefined } }]);
    expect(heater).toEqual({
      id: "dev1",
      name: "Heater",
      kind: "climate",
      product: "Panel",
      online: true,
      values: [
        { code: "power", type: "Boolean", writable: true, value: true, range: undefined, min: undefined, max: undefined, step: undefined, unit: undefined, scale: undefined },
        { code: "2", type: "Integer", writable: true, value: 21, range: undefined, min: 5, max: 30, step: 1, unit: "C", scale: 1 },
        { code: "mode", type: "Enum", writable: true, value: "auto", range: ["auto", "heat"], min: undefined, max: undefined, step: undefined, unit: undefined, scale: undefined },
        { code: "label", type: "String", writable: false, value: "kitchen", range: undefined, min: undefined, max: undefined, step: undefined, unit: undefined, scale: undefined },
        { code: "fault", type: "Boolean", writable: false, value: null, range: undefined, min: undefined, max: undefined, step: undefined, unit: undefined, scale: undefined },
      ],
    });
    expect(sensor.kind).toBeUndefined();
    expect(sensor.product).toBeUndefined();
    expect(sensor.online).toBe(false);
    expect(sensor.values[0]).toMatchObject({ code: "temp", type: "Number", writable: false, value: 3 });
  });

  it("sends the bearer token and a JSON content type only when there's something to send", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "192.168.1.20:9000", token: "S3CRET" }, fetch: () => ({ body: [] }) });
    def.devices.poll(host.ctx);
    expect(host.fetches[0].options.headers).toEqual({ ...headers, Authorization: "Bearer S3CRET" });
  });

  it("refuses a device list that isn't an array", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "192.168.1.20:9000" }, fetch: () => ({ body: { devices: [] } }) });
    expect(() => def.devices.poll(host.ctx)).toThrow("the helper's /devices must be a JSON array of devices");
  });

  it("refuses a device without an id, a name or dps", async () => {
    const def = await load();
    const bad = [
      [{ name: "x", dps: [] }],
      [{ id: "", name: "x", dps: [] }],
      [{ id: "d", dps: [] }],
      [{ id: "d", name: "x" }],
    ];
    for (const body of bad) {
      const host = makeHost({ settings: { host: "h" }, fetch: () => ({ body }) });
      expect(() => def.devices.poll(host.ctx)).toThrow("the helper sent a device without an id, a name or dps");
    }
  });

  it("refuses a data point with no dp or code", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h" }, fetch: () => ({ body: [{ id: "d1", name: "x", dps: [{ type: "Boolean", value: true }] }] }) });
    expect(() => def.devices.poll(host.ctx)).toThrow("device d1 has a data point without a dp or code");
    const numericCode = makeHost({ settings: { host: "h" }, fetch: () => ({ body: [{ id: "d1", name: "x", dps: [{ dp: "1", code: 5, type: "Boolean" }] }] }) });
    expect(() => def.devices.poll(numericCode.ctx)).toThrow("device d1 has a data point without a dp or code");
  });

  it("writes a value to the device's data point, found by the code it was polled under", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "192.168.1.20:9000" }, fetch: (url) => (url.endsWith("/devices") ? { body: devices } : { body: "" }) });
    def.devices.poll(host.ctx);
    def.devices.write(host.ctx, "dev1", "mode", "heat");
    expect(host.fetches[1]).toEqual({
      url: `${base}/devices/dev1/dps/3`,
      options: { method: "POST", headers: { ...headers, "Content-Type": "application/json" }, body: '{"value":"heat"}' },
    });
  });

  it("escapes the device id and data point in the write path", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h" }, fetch: (url) => (url.endsWith("/devices") ? { body: devices } : { status: 204 }) });
    def.devices.poll(host.ctx);
    def.devices.write(host.ctx, "a b/c", "temp", 4);
    expect(host.fetches[1].url).toBe("http://h/devices/a%20b%2Fc/dps/t");
  });

  it("refuses a write to a code it never polled", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h" }, fetch: () => ({ body: devices }) });
    def.devices.poll(host.ctx);
    expect(() => def.devices.write(host.ctx, "dev1", "nope", 1)).toThrow("the helper has no nope on dev1");
    expect(() => def.devices.write(host.ctx, "ghost", "power", true)).toThrow("the helper has no power on ghost");
  });

  it("reports a write the helper rejects", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h" }, fetch: (url) => (url.endsWith("/devices") ? { body: devices } : { status: 500 }) });
    def.devices.poll(host.ctx);
    expect(() => def.devices.write(host.ctx, "dev1", "power", false)).toThrow("the helper answered 500 to POST /devices/dev1/dps/1");
  });

  it("says when the helper refuses the token, on any call", async () => {
    const def = await load();
    const polled = makeHost({ settings: { host: "h", token: "x" }, fetch: () => ({ status: 401 }) });
    expect(() => def.devices.poll(polled.ctx)).toThrow("the helper refused the token (401)");
    const forbidden = makeHost({ settings: { host: "h", token: "x", data: true }, fetch: () => ({ status: 403 }) });
    expect(() => def.data.fetch(forbidden.ctx)).toThrow("the helper refused the token (403)");
  });

  it("reads /data only when the owner turns it on", async () => {
    const def = await load();
    const off = makeHost({ settings: { host: "h", data: false }, fetch: () => ({ body: { x: 1 } }) });
    expect(def.data.fetch(off.ctx)).toBeNull();
    expect(off.fetches).toEqual([]);

    const on = makeHost({ settings: { host: "h", data: true }, fetch: () => ({ body: { x: 1 } }) });
    expect(def.data.fetch(on.ctx)).toEqual({ x: 1 });
    expect(on.fetches[0].url).toBe("http://h/data");
  });

  it("reports a helper that answers /data with an error", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h", data: true }, fetch: () => ({ status: 502 }) });
    expect(() => def.data.fetch(host.ctx)).toThrow("the helper answered 502 to GET /data");
  });
});
