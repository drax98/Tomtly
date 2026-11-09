import { describe, expect, it, vi } from "vitest";
import { valuesByCode } from "./harness";
import { load, HELLO, IP, Miio, properties, network, devicesFile, device, LAMP_VALUES } from "./xiaomi-fake";

describe("xiaomi: a device with a table", () => {
  it("reads every property in one call and shows the values with their names", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    const [card] = def.devices.poll(host.ctx);

    expect(card).toMatchObject({ id: "1234567", name: "Desk lamp", kind: "light", product: "yeelink.light.ceiling5", online: true });
    const values = valuesByCode(card);
    expect(values.power).toMatchObject({ type: "Boolean", writable: true, value: true });
    expect(values.brightness).toMatchObject({ type: "Integer", writable: true, value: 60, min: 1, max: 100, step: 1, unit: "%" });
    expect(values.color_temperature).toMatchObject({ value: 4000, min: 2700, max: 5700, unit: "K" });
    expect(values.mode).toMatchObject({ type: "Enum", writable: true, value: "night", range: ["day", "night"] });
    expect(values.flex_switch).toMatchObject({ value: false });
    expect(values.command).toMatchObject({ type: "Action", value: null, range: ["toggle"] });

    expect(lamp.methods("get_properties")).toHaveLength(1);
    expect(lamp.methods("get_properties")[0].params).toEqual([
      { did: "2.1", siid: 2, piid: 1 },
      { did: "2.2", siid: 2, piid: 2 },
      { did: "2.4", siid: 2, piid: 4 },
      { did: "2.3", siid: 2, piid: 3 },
      { did: "2.5", siid: 2, piid: 5 },
    ]);
    // The protocol: the hello, then the request, both on the device's port with the token's keys.
    expect(host.udpRequests[0]).toMatchObject({ host: IP, port: 54321, data: HELLO });
    expect(lamp.calls[0].stamp).toBe(1001);
  });

  it("reads a long list in batches of fifteen", async () => {
    const def = await load();
    const purifier = new Miio(IP, 1234567, properties({}));
    const host = network([purifier], { settings: { devices: devicesFile(device({ model: "zhimi.airp.mb5" })) } });
    def.devices.poll(host.ctx);
    const sizes = purifier.methods("get_properties").map((c) => c.params.length);
    expect(sizes).toEqual([15, 9]);
  });

  it("shows a device as offline after its read fails, keeps its last values, and waits before asking again", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx);

    lamp.silent = true;
    const [offline] = def.devices.poll(host.ctx);
    expect(offline.online).toBe(false);
    expect(valuesByCode(offline).brightness.value).toBe(60);
    expect(host.logs).toHaveLength(1);
    expect(host.logs[0]).toMatch(/^Desk lamp is offline: /);

    const before = lamp.methods("get_properties").length;
    def.devices.poll(host.ctx); // within 30 seconds: not asked again
    expect(lamp.methods("get_properties")).toHaveLength(before);

    vi.setSystemTime(1_000_000 + 31_000);
    lamp.silent = false;
    const [back] = def.devices.poll(host.ctx);
    expect(back.online).toBe(true);
    expect(host.logs.filter((l) => l.includes("is offline"))).toHaveLength(1); // the transition, not every poll
  });

  it("doesn't mark a device offline for one lost reply, which is asked for again", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    lamp.dropOnce.add("get_properties");
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    const [card] = def.devices.poll(host.ctx);
    expect(card.online).toBe(true);
    expect(lamp.methods("get_properties")).toHaveLength(2);
  });

  it("treats a reply that fails its checksum, or answers another request, as lost", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx);
    lamp.corrupt = true;
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(host.logs.join("\n")).toContain("get_properties: no authentic answer");

    lamp.corrupt = false;
    lamp.wrongId = true;
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
  });

  it("accepts a reply that ends with a NUL byte, as some firmware sends", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    lamp.nulSuffix = true;
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(def.devices.poll(host.ctx)[0].online).toBe(true);
  });

  it("refuses a device whose reply carries an error, and says why", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx);
    lamp.answer = (method) => (method === "get_properties" ? { error: { code: -5001, message: "device busy" } } : undefined);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(host.logs[0]).toContain('get_properties: {"code":-5001,"message":"device busy"}');
  });

  it("refuses a read reply that isn't a list", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx);
    lamp.answer = (method) => (method === "get_properties" ? { result: "ok" } : undefined);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(host.logs[0]).toContain("get_properties: unexpected reply");
  });

  it("fails a device whose hello reply is not a miIO hello", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx);
    lamp.badHello = true;
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(host.logs[0]).toContain("the device sent a bad hello reply");
  });

  it("finds a device that moved to another address on its /24, by its id, after a minute of failures", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx);

    lamp.ip = "192.168.1.77";
    def.devices.poll(host.ctx); // first failure starts the count
    vi.setSystemTime(1_000_000 + 61_000);
    const [card] = def.devices.poll(host.ctx);

    expect(card.online).toBe(true);
    expect(host.logs).toContain("Desk lamp found at a new address: 192.168.1.60 is now 192.168.1.77");
    expect(host.udpScans.some((s) => s.data === HELLO)).toBe(true);
  });

  it("doesn't look for a device whose id is too large for miIO's header", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const host = network([], { settings: { devices: devicesFile(device({ did: "5000000000" })) } });
    def.devices.poll(host.ctx);
    vi.setSystemTime(1_000_000 + 61_000);
    def.devices.poll(host.ctx);
    expect(host.udpScans).toHaveLength(0);
  });

  it("doesn't look for a device again within five minutes of the last look", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const host = network([], { settings: { devices: devicesFile(device()) } });
    def.devices.poll(host.ctx); // one failure: the count starts
    vi.setSystemTime(1_000_000 + 61_000);
    def.devices.poll(host.ctx); // first look, nothing answers
    const looks = host.udpScans.length;
    expect(looks).toBe(1);
    vi.setSystemTime(1_000_000 + 2 * 61_000);
    def.devices.poll(host.ctx);
    expect(host.udpScans).toHaveLength(looks);
  });

  it("does nothing with a device it isn't asked to read, when it's the owner's removed one", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device()) }, removed: ["1234567"] } as any);
    def.devices.poll(host.ctx);
    expect(lamp.calls).toHaveLength(0);
  });
});
