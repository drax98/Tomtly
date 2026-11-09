import { describe, expect, it, vi } from "vitest";
import { crypto, makeHost, TIMEOUT } from "./harness";
import { MAC, ADDRESS, load, Unit, network } from "./gree-fake";

describe("gree: finding a moved unit", () => {
  it("finds the unit at its new address once it has been failing for a minute", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = network([unit], { mac: MAC });
    def.devices.poll(host.ctx);

    unit.address = "192.168.1.77";
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT); // too soon to look
    vi.setSystemTime(1_000_000 + 61_000);
    const [device] = def.devices.poll(host.ctx);

    expect(device.id).toBe(MAC);
    expect(host.logs).toContain("the air conditioner moved from 192.168.1.44 to 192.168.1.77");
    expect(host.udpScans).toHaveLength(1);
    expect(host.udpScans[0].data).toBe(crypto.hex(JSON.stringify({ t: "scan" })));
    expect(host.udpRequests.at(-1)?.host).toBe("192.168.1.77");
  });

  it("takes the only unit it finds when no MAC is known", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const unit = new Unit("192.168.1.90");
    const host = network([unit]);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT); // never connected, so no MAC is known
    vi.setSystemTime(1_000_000 + 61_000);
    expect(def.devices.poll(host.ctx)[0].id).toBe(MAC);
    expect(host.logs).toContain("the air conditioner moved from 192.168.1.44 to 192.168.1.90");
  });

  it("doesn't guess between several units when no MAC is known", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const busy = network([new Unit("192.168.1.90", MAC), new Unit("192.168.1.91", "112233445566")]);
    expect(() => def.devices.poll(busy.ctx)).toThrow(TIMEOUT);
    vi.setSystemTime(1_000_000 + 61_000);
    expect(() => def.devices.poll(busy.ctx)).toThrow(TIMEOUT);
    expect(busy.logs.some((l) => l.includes("2 units answered and the MAC isn't known"))).toBe(true);
    expect(busy.logs.some((l) => l.includes("moved"))).toBe(false);
  });

  it("does nothing when the unit with its MAC isn't on the network", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = network([unit], { mac: "001122334455" });
    def.devices.poll(host.ctx);
    unit.address = "192.168.1.90";
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    vi.setSystemTime(1_000_000 + 61_000);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    expect(host.udpScans).toHaveLength(1);
    expect(host.logs.some((l) => l.includes("moved"))).toBe(false);
  });

  it("waits five minutes between looks", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    const host = network([]);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT); // first failure, the look starts counting
    vi.setSystemTime(1_000_000 + 61_000);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    expect(host.udpScans).toHaveLength(1);
    vi.setSystemTime(1_000_000 + 120_000);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    expect(host.udpScans).toHaveLength(1);
    vi.setSystemTime(1_000_000 + 61_000 + 5 * 60_000);
    expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
    expect(host.udpScans).toHaveLength(2);
  });

  it("doesn't look on a public address, or one that isn't an IP", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1_000_000);
    const def = await load();
    for (const address of ["8.8.8.8", "aircon.local"]) {
      const host = makeHost({ settings: { host: address, name: "x" }, udpRequest: () => { throw new Error(TIMEOUT); } });
      expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
      vi.setSystemTime(1_000_000 + 61_000);
      expect(() => def.devices.poll(host.ctx)).toThrow(TIMEOUT);
      expect(host.udpScans).toHaveLength(0);
      vi.setSystemTime(1_000_000);
    }
  });
});

describe("gree: discovery", () => {
  it("lists the unit with its MAC as this integration's, and any other as another", async () => {
    const def = await load();
    const mine = new Unit(ADDRESS, MAC);
    const other = new Unit("192.168.1.45", "112233445566");
    const host = network([mine, other], { mac: MAC });
    def.devices.poll(host.ctx);
    const found = def.devices.discover(host.ctx);
    expect(found).toEqual([
      { id: MAC, name: "Air conditioner", product: "Gree-based AC", address: ADDRESS },
      {
        id: "112233445566",
        name: "Another Gree air conditioner",
        product: "Gree-based AC",
        address: "192.168.1.45",
        note: "This integration runs one air conditioner, the one at its address setting.",
      },
    ]);
  });

  it("scans every other address on the unit's /24", async () => {
    const def = await load();
    const host = network([]);
    def.devices.discover(host.ctx);
    const hosts = host.udpScans[0].host.split(",");
    expect(hosts).toHaveLength(254);
    expect(hosts[0]).toBe("192.168.1.1");
    expect(hosts.at(-1)).toBe("192.168.1.254");
  });

  it("takes a unit at the setting's address as its own when no MAC is known", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS, MAC), new Unit("192.168.1.9", "112233445566")]);
    const found = def.devices.discover(host.ctx);
    expect(found.map((f: FoundDevice) => [f.id, f.note === undefined])).toEqual([
      [MAC, true],
      ["112233445566", false],
    ]);
  });

  it("refuses to look on a public address", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "8.8.8.8", name: "x" } });
    expect(() => def.devices.discover(host.ctx)).toThrow("its address isn't on a private network, so there's nothing to look through");
  });

  it("ignores a reply that isn't a Gree unit", async () => {
    const def = await load();
    const host = makeHost({
      settings: { host: ADDRESS, name: "x" },
      udpScan: () => [
        { from: "192.168.1.5", data: crypto.hex("not json") },
        { from: "192.168.1.6", data: crypto.hex(JSON.stringify({ t: "pack" })) },
      ],
    });
    expect(def.devices.discover(host.ctx)).toEqual([]);
  });
});
