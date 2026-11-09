import { describe, expect, it } from "vitest";
import { crypto, makeHost, TIMEOUT, valuesByCode } from "./harness";
import { ECB_KEY, GCM_KEY, UNIT_KEY, MAC, ADDRESS, load, Unit, network, ALL_COLS } from "./gree-fake";

describe("gree: reading the unit", () => {
  for (const gcm of [false, true]) {
    it(`connects with the ${gcm ? "GCM" : "ECB"} scheme and reports every value the unit has`, async () => {
      const def = await load();
      const unit = new Unit(ADDRESS, MAC, { gcm });
      const host = network([unit]);
      const [device] = def.devices.poll(host.ctx);

      expect(device).toMatchObject({ id: MAC, name: "Air conditioner", kind: "climate", product: "Gree-based AC" });
      const values = valuesByCode(device);
      expect(values.power).toEqual({ code: "power", type: "Boolean", writable: true, value: true });
      expect(values.mode).toMatchObject({ type: "Enum", writable: true, value: "heat", range: ["auto", "cool", "dry", "fan", "heat"] });
      expect(values.temp_set).toMatchObject({ type: "Integer", writable: true, value: 24, min: 16, max: 30, step: 1, unit: "°C" });
      expect(values.fan_speed).toMatchObject({ type: "Enum", writable: true, value: "medium" });
      expect(values.quiet).toMatchObject({ type: "Boolean", writable: true, value: false });
      expect(values.turbo).toMatchObject({ value: false });
      expect(values.light).toMatchObject({ value: true });
      expect(values.temp_current).toEqual({ code: "temp_current", type: "Integer", value: 23, unit: "°C" });
      expect(values.swing_vertical).toMatchObject({ value: "full" });
      expect(values.swing_horizontal).toMatchObject({ value: "right" });
      expect(values.x_fan).toEqual({ code: "x_fan", type: "Boolean", writable: true, value: true });
      expect(values.fresh_air).toBeUndefined(); // the unit doesn't report it, so it isn't shown

      // The protocol: a scan, a bind with the published key, then one status read for every column.
      expect(unit.requestsOf("bind")).toHaveLength(1);
      expect(unit.requestsOf("bind")[0].env.tag !== undefined).toBe(gcm);
      expect(unit.requestsOf("status")[0].body).toEqual({ t: "status", mac: MAC, cols: ALL_COLS });
      expect(unit.requestsOf("status")[0].key).toBe(UNIT_KEY);
      expect(unit.requestsOf("status")[0].env).toMatchObject({ t: "pack", i: 0, uid: 0, cid: "app", tcid: MAC });
    });
  }

  it("binds with the generic key first and then the unit's own key", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    def.devices.poll(network([unit]).ctx);
    expect(unit.requestsOf("bind")[0]).toMatchObject({ key: ECB_KEY, body: { t: "bind", mac: MAC, uid: 0 } });
  });

  it("uses GCM for the bind when the scan replies with a tag, and says so in the bind", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS, MAC, { gcm: true });
    def.devices.poll(network([unit]).ctx);
    expect(unit.requestsOf("bind")[0]).toMatchObject({ key: GCM_KEY, body: { t: "bind", mac: MAC, uid: 0, cid: MAC } });
  });

  it("falls back to the other scheme when the bind under the scan's scheme gets silence", async () => {
    const def = await load();
    // The scan carries a GCM tag, but this unit binds over ECB only: the GCM bind is silence, and the ECB one works.
    const unit = new Unit(ADDRESS, MAC, { scanTag: true });
    const host = network([unit]);
    const [device] = def.devices.poll(host.ctx);
    expect(device.id).toBe(MAC);
    expect(unit.requestsOf("bind").map((r) => r.key)).toEqual([ECB_KEY]);
    expect(host.udpRequests).toHaveLength(5); // the scan, two GCM bind tries, one ECB bind, one status
  });

  it("gives up on a unit that never answers the scan, after three tries", async () => {
    const def = await load();
    const host = network([]);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    expect(host.udpRequests).toHaveLength(3);
    expect(host.udpRequests.every((r) => r.options?.timeoutMs === 1500)).toBe(true);
  });

  it("refuses a scan reply that has no MAC", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { scanWithoutMac: true })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner's scan reply has no MAC");
  });

  it("refuses a scan reply with no data in it", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = makeHost({
      settings: { host: ADDRESS, name: "x" },
      udpRequest: () => crypto.hex(JSON.stringify({ t: "pack" })),
    });
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner sent a reply with no data");
    expect(unit.binds).toBe(0);
  });

  it("says the unit won't accept a bind when every scheme's bind is refused", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { refuseBind: true })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner didn't accept a bind: is another app pairing with it?");
  });

  it("moves past a bind reply it can't decrypt, to the next scheme", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { garbageBind: true })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner didn't accept a bind");
  });

  it("does not accept an empty key in a bind reply", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { emptyKeyBind: true })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner didn't accept a bind");
  });

  it("stops a bind on an error that isn't a lost or undecryptable reply", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { noPackBind: true })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner sent a reply with no data");
  });

  it("refuses a status reply that isn't a data reply", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { statusReply: { t: "res", r: 200 } })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner sent an unexpected status reply (res)");
  });

  it("refuses a status reply whose columns and data don't line up", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { statusReply: { t: "dat", cols: ["Pow"], dat: [1, 2] } })]);
    expect(() => def.devices.poll(host.ctx)).toThrow("the air conditioner sent an unexpected status reply (dat)");
  });

  it("ignores a column whose value isn't a number", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC, { statusReply: { t: "dat", cols: ["Pow", "Mod"], dat: ["on", 4] } })]);
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.power.value).toBeNull();
    expect(values.mode.value).toBe("heat");
  });

  it("shows only the features the unit reports, and an unknown mode as its number", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS, MAC, { lacks: ["SwUpDn", "SwingLfRig", "Blo", "Tur"], state: { Mod: 9 } });
    const values = valuesByCode(def.devices.poll(network([unit]).ctx)[0]);
    expect(values.swing_vertical).toBeUndefined();
    expect(values.swing_horizontal).toBeUndefined();
    expect(values.x_fan).toBeUndefined();
    expect(values.turbo).toMatchObject({ value: null });
    expect(values.mode.value).toBe("9");
  });

  it("reads the room sensor as Celsius, with and without the unit's +40 offset", async () => {
    const def = await load();
    const reading = (raw: Record<string, number>) => {
      const unit = new Unit(ADDRESS, MAC, { state: raw });
      return valuesByCode(def.devices.poll(network([unit]).ctx)[0]).temp_current.value;
    };
    expect(reading({ TemSen: 63 })).toBe(23);
    expect(reading({ TemSen: 25 })).toBe(25);
    expect(reading({ TemSen: 0 })).toBeNull();
  });

  it("reports the room sensor as unknown when the unit doesn't send it", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS, MAC, { lacks: ["TemSen"] });
    expect(valuesByCode(def.devices.poll(network([unit]).ctx)[0]).temp_current.value).toBeNull();
  });

  it("reports the unit's power and settings as unknown when it leaves them out", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS, MAC, { lacks: ["Pow", "SetTem", "Lig"] });
    const values = valuesByCode(def.devices.poll(network([unit]).ctx)[0]);
    expect(values.power.value).toBeNull();
    expect(values.temp_set.value).toBeNull();
    expect(values.light.value).toBeNull();
  });

  it("reconnects after a poll fails, on the next one", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = network([unit]);
    def.devices.poll(host.ctx);
    unit.silent = true;
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    unit.silent = false;
    const [device] = def.devices.poll(host.ctx);
    expect(device.id).toBe(MAC);
    expect(unit.binds).toBe(2);
  });

  it("returns nothing for a unit the owner removed, once it has been seen", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    def.devices.poll(network([unit]).ctx);
    const removed = network([unit], {}, { removed: [MAC] });
    expect(def.devices.poll(removed.ctx)).toEqual([]);
    expect(removed.udpRequests).toHaveLength(0);
  });
});
