import { describe, expect, it } from "vitest";
import { crypto, TIMEOUT } from "./harness";
import { MAC, ADDRESS, load, Unit, network } from "./gree-fake";

describe("gree: commands", () => {
  for (const gcm of [false, true]) {
    it(`sends each setting in the unit's own words, over the ${gcm ? "GCM" : "ECB"} scheme`, async () => {
      const def = await load();
      const unit = new Unit(ADDRESS, MAC, { gcm });
      const host = network([unit]);
      def.devices.poll(host.ctx);
      const cases: [string, boolean | number | string, string[], number[]][] = [
        ["power", true, ["Pow"], [1]],
        ["power", false, ["Pow"], [0]],
        ["mode", "heat", ["Mod"], [4]],
        ["mode", 2, ["Mod"], [2]],
        ["temp_set", 23.6, ["TemUn", "SetTem"], [0, 24]],
        ["fan_speed", "high", ["WdSpd"], [5]],
        ["quiet", true, ["Quiet"], [1]],
        ["turbo", false, ["Tur"], [0]],
        ["light", true, ["Lig"], [1]],
        ["swing_vertical", "swing_top", ["SwUpDn"], [11]],
        ["swing_horizontal", "left", ["SwingLfRig"], [2]],
        ["fresh_air", true, ["Air"], [1]],
        ["x_fan", false, ["Blo"], [0]],
        ["health", true, ["Health"], [1]],
        ["sleep", true, ["SwhSlp"], [1]],
        ["frost_protection", true, ["StHt"], [1]],
        ["energy_saving", true, ["SvSt"], [1]],
      ];
      for (const [code, value, opt, p] of cases) {
        def.devices.write(host.ctx, MAC, code, value);
        const last = unit.requestsOf("cmd").at(-1)!;
        expect(last.body, code).toEqual({ t: "cmd", opt, p });
        expect(last.env).toMatchObject({ t: "pack", i: 0, uid: 0, cid: "app", tcid: MAC });
      }
      // The commands were the same session: one bind, however many writes.
      expect(unit.binds).toBe(1);
    });
  }

  it("connects before the first command when nothing is connected yet", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = network([unit]);
    def.devices.write(host.ctx, MAC, "power", false);
    expect(unit.binds).toBe(1);
    expect(unit.requestsOf("cmd")[0].body).toEqual({ t: "cmd", opt: ["Pow"], p: [0] });
  });

  it("refuses a value the code doesn't take", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS)]);
    def.devices.poll(host.ctx);
    expect(() => def.devices.write(host.ctx, MAC, "mode", "warp")).toThrow("warp isn't a mode this air conditioner has");
    expect(() => def.devices.write(host.ctx, MAC, "power", "on")).toThrow("on isn't a power this air conditioner has");
  });

  it("refuses a code that isn't one of its values", async () => {
    const def = await load();
    const host = network([new Unit(ADDRESS)]);
    expect(() => def.devices.write(host.ctx, MAC, "humidity", 20)).toThrow("the air conditioner has no value called humidity");
  });

  it("refuses a command for another air conditioner", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = network([unit]);
    def.devices.poll(host.ctx);
    expect(() => def.devices.write(host.ctx, "ffeeddccbbaa", "power", true)).toThrow("unknown air conditioner ffeeddccbbaa");
    expect(unit.requestsOf("cmd")).toHaveLength(0);
  });

  it("reports a command the unit rejects, and binds again for the next one", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS, MAC, { rejectCommands: true });
    const host = network([unit]);
    def.devices.poll(host.ctx);
    expect(() => def.devices.write(host.ctx, MAC, "power", true)).toThrow("the air conditioner rejected the command (res, 400)");
    expect(unit.binds).toBe(1);
    unit.o.rejectCommands = false;
    def.devices.write(host.ctx, MAC, "power", true);
    expect(unit.binds).toBe(2);
    expect(unit.state.Pow).toBe(1);
  });

  it("reports a command that gets no answer, and connects again next time", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS);
    const host = network([unit]);
    def.devices.poll(host.ctx);
    unit.silent = true;
    expect(() => def.devices.write(host.ctx, MAC, "power", true)).toThrow(TIMEOUT);
    unit.silent = false;
    def.devices.write(host.ctx, MAC, "power", true);
    expect(unit.binds).toBe(2);
  });

  it("reports a command when the unit can't be reached at all", async () => {
    const def = await load();
    const host = network([]);
    expect(() => def.devices.write(host.ctx, MAC, "power", true)).toThrow(TIMEOUT);
  });
});

describe("gree: the protocol's own checks", () => {
  it("rejects a reply whose data isn't valid under either key", async () => {
    const def = await load();
    const unit = new Unit(ADDRESS, MAC, { gcm: true });
    const host = network([unit]);
    def.devices.poll(host.ctx);
    // A reply sealed under a key the extension doesn't hold fails authentication, and the poll says so.
    unit.answer = () => crypto.hex(JSON.stringify({ t: "pack", pack: Buffer.alloc(16, 1).toString("base64"), tag: Buffer.alloc(16, 2).toString("base64") }));
    expect(() => def.devices.poll(host.ctx)).toThrow();
  });
});
