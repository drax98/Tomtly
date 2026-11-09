import { describe, expect, it } from "vitest";
import { valuesByCode } from "./harness";
import { load, IP, type Answer, Miio, properties, network, devicesFile, device, LAMP_VALUES } from "./xiaomi-fake";

describe("xiaomi: commands to a table device", () => {
  it("sends a property change as set_properties and reads the device's answer", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method, params) => {
      if (method === "set_properties") return { result: params.map((p: any) => ({ did: p.did, code: 0 })) };
      return properties(LAMP_VALUES)(method, params);
    });
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.write(host.ctx, "1234567", "brightness", 42);
    def.devices.write(host.ctx, "1234567", "power", false);
    def.devices.write(host.ctx, "1234567", "mode", "day");
    expect(lamp.methods("set_properties").map((c) => c.params[0])).toEqual([
      { did: "2.2", siid: 2, piid: 2, value: 42 },
      { did: "2.1", siid: 2, piid: 1, value: false },
      { did: "2.3", siid: 2, piid: 3, value: 1 },
    ]);
  });

  it("accepts an answer of 1, which means accepted and still working on it", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method) => (method === "set_properties" ? { result: [{ code: 1 }] } : undefined));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(() => def.devices.write(host.ctx, "1234567", "power", true)).not.toThrow();
  });

  it("reports a change the device refuses", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method) => (method === "set_properties" ? { result: [{ code: -4003 }] } : undefined));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(() => def.devices.write(host.ctx, "1234567", "power", true)).toThrow('power: device refused: [{"code":-4003}]');
  });

  it("reports a change answered with something other than a list", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method) => (method === "set_properties" ? { result: [] } : undefined));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(() => def.devices.write(host.ctx, "1234567", "power", true)).toThrow("power: device refused");
  });

  it("sends an action with its service and action ids", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method) => (method === "action" ? { result: { code: 0 } } : undefined));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.write(host.ctx, "1234567", "command", "toggle");
    expect(lamp.methods("action")[0].params).toEqual({ did: "call-2-1", siid: 2, aiid: 1, in: [] });
  });

  it("refuses an action the device doesn't take, or refuses it", async () => {
    const def = await load();
    const refusing = new Miio(IP, 1234567, (method) => (method === "action" ? { result: { code: -4001 } } : undefined));
    const host = network([refusing], { settings: { devices: devicesFile(device()) } });
    expect(() => def.devices.write(host.ctx, "1234567", "command", "toggle")).toThrow("toggle: device refused");
    expect(() => def.devices.write(host.ctx, "1234567", "command", "explode")).toThrow("command has no action explode");
  });

  it("doesn't resend an action whose reply was lost, since it may have happened", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method) => (method === "action" ? { result: { code: 0 } } : undefined));
    lamp.dropOnce.add("action");
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(() => def.devices.write(host.ctx, "1234567", "command", "toggle")).toThrow(/i\/o timeout/);
    expect(lamp.methods("action")).toHaveLength(1);
  });

  it("resends a change whose reply was lost, once", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method, params) => (method === "set_properties" ? { result: [{ code: 0 }] } : undefined));
    lamp.dropOnce.add("set_properties");
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    def.devices.write(host.ctx, "1234567", "power", true);
    expect(lamp.methods("set_properties")).toHaveLength(2);
  });

  it("refuses a code that doesn't exist, and a device it doesn't know", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, (method, params) => (method === "set_properties" ? { result: [{ code: 0 }] } : properties(LAMP_VALUES)(method, params)));
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(() => def.devices.write(host.ctx, "1234567", "nope", 1)).toThrow("nope is not writable");
    expect(() => def.devices.write(host.ctx, "1234567", "flex_switch", true)).not.toThrow();
    expect(() => def.devices.write(host.ctx, "7654321", "power", true)).toThrow("unknown device 7654321");
  });

  it("refuses a read-only value, and a value of a model whose spec wrote it write-only", async () => {
    const def = await load();
    const purifier = new Miio(IP, 1234567, properties({}));
    const host = network([purifier], { settings: { devices: devicesFile(device({ model: "zhimi.airp.mb5" })) } });
    expect(() => def.devices.write(host.ctx, "1234567", "pm25", 3)).toThrow("pm25 is not writable");
  });
});

describe("xiaomi: a legacy family and older devices", () => {
  it("reads an older light with its own get_prop names, and converts each reply", async () => {
    const def = await load();
    const light = new Miio(IP, 1234567, (method) =>
      method === "get_prop" ? { result: ["on", "50", "4000", "16711680", "1", "off"] } : undefined,
    );
    const host = network([light], { settings: { devices: devicesFile(device({ model: "yeelink.light.lamp2" })) } });
    const [card] = def.devices.poll(host.ctx);
    const values = valuesByCode(card);
    expect(card).toMatchObject({ kind: "light", online: true });
    expect(values.power.value).toBe(true);
    expect(values.brightness.value).toBe(50);
    expect(values.color_temperature.value).toBe(4000);
    expect(values.color.value).toBe("#ff0000");
    expect(values.color_mode.value).toBe(1);
    expect(values.flowing.value).toBe(false);
    expect(light.methods("get_prop")[0].params).toEqual(["power", "bright", "ct", "rgb", "color_mode", "flowing"]);
  });

  it("sends a legacy change with the family's extra arguments", async () => {
    const def = await load();
    const light = new Miio(IP, 1234567, (method) => (method === "set_power" || method === "set_bright" ? { result: ["ok"] } : undefined));
    const host = network([light], { settings: { devices: devicesFile(device({ model: "yeelink.light.lamp2" })) } });
    def.devices.write(host.ctx, "1234567", "power", true);
    def.devices.write(host.ctx, "1234567", "brightness", 30);
    expect(light.methods("set_power")[0].params).toEqual(["on", "smooth", 500]);
    expect(light.methods("set_bright")[0].params).toEqual([30, "smooth", 500]);
  });

  it("refuses a legacy change the device answers with anything but ok", async () => {
    const def = await load();
    const light = new Miio(IP, 1234567, (method) => (method === "set_power" ? { result: ["fail"] } : undefined));
    const host = network([light], { settings: { devices: devicesFile(device({ model: "yeelink.light.lamp2" })) } });
    expect(() => def.devices.write(host.ctx, "1234567", "power", false)).toThrow('power: device refused: ["fail"]');
  });

  it("refuses an older device that knows none of its family's properties", async () => {
    const def = await load();
    const light = new Miio(IP, 1234567, (method) => (method === "get_prop" ? { result: ["on", "50", "4000", "16711680", "1", "off"] } : undefined));
    const host = network([light], { settings: { devices: devicesFile(device({ model: "yeelink.light.lamp2" })) } });
    def.devices.poll(host.ctx);
    light.answer = (method) => (method === "get_prop" ? { result: [null, null, null, null, null, null] } : undefined);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(host.logs.join(" ")).toContain("it knows none of this family's properties");
  });

  it("reads a gateway's own calls, which take no arguments", async () => {
    const def = await load();
    const gateway = new Miio(IP, 1234567, (method) => {
      if (method === "get_arming") return { result: [1] };
      if (method === "get_rgb") return { result: [255] };
      return undefined;
    });
    const host = network([gateway], { settings: { devices: devicesFile(device({ model: "lumi.gateway.v3" })) } });
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.arming.value).toBe(true);
    expect(values.light_color.value).toBe("#0000ff");
  });

  it("leaves a gateway call that's refused unknown, and marks the gateway offline when a call gets no answer", async () => {
    const def = await load();
    const gateway = new Miio(IP, 1234567, (method) => (method === "get_arming" ? { error: { code: -1, message: "no" } } : undefined));
    const host = network([gateway], { settings: { devices: devicesFile(device({ model: "lumi.gateway.v3" })) } });
    expect(() => def.devices.poll(host.ctx)).not.toThrow();
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
  });
});

describe("xiaomi: a Roborock vacuum", () => {
  const status = [{ fan_power: 102, state: 5, battery: 88, error_code: 0, clean_time: 600, clean_area: 2400000, dnd_enabled: 0, map_present: 1 }];
  const vacuum = (extra: (method: string, params: any) => Answer = () => undefined) =>
    new Miio(IP, 1234567, (method, params) => {
      if (method === "get_status") return { result: status };
      if (method === "get_consumable") return { result: [{ main_brush_work_time: 360000, side_brush_work_time: 7200, filter_work_time: 0, sensor_dirty_time: 1800 }] };
      if (method === "get_clean_summary") return { result: [{ clean_time: 36000, clean_area: 5000000, clean_count: 12 }] };
      if (method === "get_sound_volume") return { result: [70] };
      return extra(method, params);
    });

  it("reads its status and extra counters", async () => {
    const def = await load();
    const v = vacuum();
    const host = network([v], { settings: { devices: devicesFile(device({ model: "roborock.vacuum.s5" })) } });
    const [card] = def.devices.poll(host.ctx);
    const values = valuesByCode(card);
    expect(card).toMatchObject({ kind: "vacuum", online: true });
    expect(values.fan_speed.value).toBe("balanced");
    expect(values.state.value).toBe("cleaning");
    expect(values.battery.value).toBe(88);
    expect(values.clean_time.value).toBe(10);
    expect(values.clean_area.value).toBe(2); // whole square metres, as an Integer
    expect(values.do_not_disturb.value).toBe(false);
    expect(values.map_present.value).toBe(true);
    expect(values.main_brush_hours.value).toBe(100);
    expect(values.side_brush_hours.value).toBe(2);
    expect(values.total_clean_hours.value).toBe(10);
    expect(values.total_clean_area.value).toBe(5);
    expect(values.clean_count.value).toBe(12);
    expect(values.sound_volume.value).toBe(70);
    expect(values.command.range).toEqual(["start", "pause", "stop", "dock", "spot", "find"]);
  });

  it("stays online when its extra counters are refused", async () => {
    const def = await load();
    const v = new Miio(IP, 1234567, (method) => {
      if (method === "get_status") return { result: status };
      return { error: { code: -1, message: "unknown" } };
    });
    const host = network([v], { settings: { devices: devicesFile(device({ model: "roborock.vacuum.s5" })) } });
    const [card] = def.devices.poll(host.ctx);
    expect(card.online).toBe(true);
    expect(valuesByCode(card).battery.value).toBe(88);
    expect(valuesByCode(card).main_brush_hours.value).toBeNull();
  });

  it("refuses an empty status", async () => {
    const def = await load();
    const v = vacuum();
    const host = network([v], { settings: { devices: devicesFile(device({ model: "roborock.vacuum.s5" })) } });
    def.devices.poll(host.ctx);
    v.answer = (method) => (method === "get_status" ? { result: [] } : undefined);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(host.logs[0]).toContain("empty vacuum status");
  });

  it("sends its own commands for a fan speed, a volume and each action", async () => {
    const def = await load();
    const v = vacuum((method) => (method.startsWith("app_") || method === "find_me" || method === "set_custom_mode" || method === "change_sound_volume" ? { result: ["ok"] } : undefined));
    const host = network([v], { settings: { devices: devicesFile(device({ model: "roborock.vacuum.s5" })) } });
    def.devices.poll(host.ctx);
    def.devices.write(host.ctx, "1234567", "fan_speed", "silent");
    def.devices.write(host.ctx, "1234567", "sound_volume", 40);
    def.devices.write(host.ctx, "1234567", "command", "start");
    def.devices.write(host.ctx, "1234567", "command", "dock");
    def.devices.write(host.ctx, "1234567", "command", "find");
    def.devices.write(host.ctx, "1234567", "command", "nothing");
    expect(v.methods("set_custom_mode")[0].params).toEqual([101]);
    expect(v.methods("change_sound_volume")[0].params).toEqual([40]);
    expect(v.methods("app_start")).toHaveLength(1);
    expect(v.methods("app_pause")).toHaveLength(1);
    expect(v.methods("app_charge")).toHaveLength(1);
    expect(v.methods("find_me")).toHaveLength(1);
  });

  it("shows a vacuum model it has no fan speed table for as a plain reading", async () => {
    const def = await load();
    const v = vacuum();
    const host = network([v], { settings: { devices: devicesFile(device({ model: "roborock.vacuum.c1" })) } });
    const [card] = def.devices.poll(host.ctx);
    expect(valuesByCode(card).fan_speed).toMatchObject({ type: "Integer", writable: false });
    expect(() => def.devices.write(host.ctx, "1234567", "fan_speed", 101)).toThrow("fan_speed is not writable");
  });
});
