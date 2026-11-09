import { describe, expect, it } from "vitest";
import { loadExtension, makeHost, valuesByCode } from "./harness";

const load = () => loadExtension(() => import("../hue/extension.ts"));
const settings = { host: "192.168.1.30", apiKey: "KEY123" };
const base = "http://192.168.1.30/api/KEY123";

describe("hue", () => {
  it("polls every light with its switch, brightness, and reachability", async () => {
    const def = await load();
    const host = makeHost({
      settings,
      fetch: () => ({
        body: {
          "1": { name: "Desk", productname: "Hue white", state: { on: true, bri: 200, reachable: true } },
          "2": { name: "Hall", productname: "Hue lamp", state: { on: false, bri: 1, reachable: false } },
        },
      }),
    });
    const devices = def.devices.poll(host.ctx);

    expect(host.fetches).toEqual([{ url: `${base}/lights`, options: {} }]);
    expect(devices).toEqual([
      {
        id: "1",
        name: "Desk",
        kind: "light",
        product: "Hue white",
        online: true,
        values: [
          { code: "switch", type: "Boolean", writable: true, value: true },
          { code: "brightness", type: "Integer", writable: true, value: 200, min: 1, max: 254 },
        ],
      },
      {
        id: "2",
        name: "Hall",
        kind: "light",
        product: "Hue lamp",
        online: false,
        values: [
          { code: "switch", type: "Boolean", writable: true, value: false },
          { code: "brightness", type: "Integer", writable: true, value: 1, min: 1, max: 254 },
        ],
      },
    ]);
  });

  it("leaves brightness out for a light that has none", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: { "3": { name: "Plug", state: { on: true, reachable: true } } } }) });
    const [light] = def.devices.poll(host.ctx);
    expect(light.values.map((v: DeviceValue) => v.code)).toEqual(["switch"]);
    expect(light.product).toBeUndefined();
  });

  it("throws when the bridge answers with an HTTP error", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ status: 503 }) });
    expect(() => def.devices.poll(host.ctx)).toThrow("the bridge answered 503");
  });

  it("throws the bridge's description when it refuses with a 200 error list", async () => {
    const def = await load();
    const host = makeHost({
      settings,
      fetch: () => ({ body: [{ error: { type: 1, description: "unauthorized user" } }] }),
    });
    expect(() => def.devices.poll(host.ctx)).toThrow("the bridge refused: unauthorized user");
  });

  it("says so when the bridge refuses with an empty list", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: [] }) });
    expect(() => def.devices.poll(host.ctx)).toThrow("the bridge refused: no reason given");
  });

  it("sends a switch command as a PUT of the on state", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: [{ success: {} }] }) });
    def.devices.write(host.ctx, "7", "switch", false);
    expect(host.fetches).toEqual([{ url: `${base}/lights/7/state`, options: { method: "PUT", body: '{"on":false}' } }]);
  });

  it("sends a brightness command as a PUT of the bri value", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: [{ success: {} }] }) });
    def.devices.write(host.ctx, "7", "brightness", 128);
    expect(host.fetches).toEqual([{ url: `${base}/lights/7/state`, options: { method: "PUT", body: '{"bri":128}' } }]);
  });

  it("throws when a command gets an HTTP error back", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ status: 401 }) });
    expect(() => def.devices.write(host.ctx, "7", "switch", true)).toThrow("the bridge answered 401");
  });

  it("reads the device values back as the same types the page expects", async () => {
    const def = await load();
    const host = makeHost({ settings, fetch: () => ({ body: { "9": { name: "Lamp", state: { on: true, bri: 10, reachable: true } } } }) });
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.switch.type).toBe("Boolean");
    expect(values.brightness.type).toBe("Integer");
  });
});
