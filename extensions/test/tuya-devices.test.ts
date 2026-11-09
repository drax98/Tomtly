import { describe, expect, it, vi } from "vitest";
import { valuesByCode } from "./harness";

// The Tuya extension, against a fake device on each protocol. The fake mirrors internal/extension/tuya_ext_test.go (3.4 and
// 3.5) and tuya_crc_fake_test.go (3.1 to 3.3), but it's driven in-process: the extension writes a frame, the connection's
// write() hands it to the device, and the device's answer is queued for the next read.
import { load, IP, NOW, send, TuyaDevice, network, device, file, settingsFor, run, gatewayFile, byId, lamp, COLOUR_MAPPING } from "./tuya-fake";

describe("tuya: discover", () => {
  it("lists what answers, the parked ones found on the network, unknown things, and devices no host reaches", async () => {
    const def = await load();
    const plug = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } });
    const entries = [device(), device({ id: "plug2", name: "Plug 2", ip: "192.168.1.60" }), device({ id: "plug3", name: "Plug 3" }), device({ id: "far", name: "Far", ip: "8.8.8.8" })];
    const hosts = `plug=${IP}@3.3,plug2=192.168.1.60@3.3,far=8.8.8.8@3.3`;
    const host = network([plug], settingsFor(file(...entries), hosts), { tcpScan: () => ["192.168.1.60", "192.168.1.200"] });
    run(def, host, 2);

    expect(def.devices.discover(host.ctx)).toEqual([
      { id: "plug", name: "Plug", product: "Plug", address: IP },
      { id: "plug2", name: "Plug 2", product: "Plug", address: "192.168.1.60" },
      { id: "address-192.168.1.200", name: "Unknown Smart Life device", address: "192.168.1.200", note: expect.stringContaining("Something answers") },
      { id: "plug3", name: "Plug 3", product: "Plug", note: expect.stringContaining("no entry in Hosts reaches it") },
    ]);
    // One scan of the private /24 the links are on, and none for the public address.
    expect(host.tcpScans).toHaveLength(1);
    expect(host.tcpScans[0].hosts).not.toContain(IP);
  });
});

describe("tuya: the devices file and hosts setting", () => {
  it.each([
    ["=1.2.3.4@3.3", 'bad host entry "=1.2.3.4@3.3", want id=ip@version'],
    ["@3.3", 'bad host entry "@3.3", want id=ip@version'],
    ["nobody=1.2.3.4@3.3", "host nobody is not in the devices file"],
    ["plug=192.168.1.50@3.6", "protocol 3.6 is not supported, only 3.1 to 3.5"],
    ["plug=192.168.1.50@2.1", "protocol 2.1 is not supported, only 3.1 to 3.5"],
  ])("hosts %s is refused: %s", async (hosts, message) => {
    const def = await load();
    const host = network([], settingsFor(file(device()), hosts));
    expect(() => def.devices.poll(host.ctx)).toThrow(message);
  });

  it("a host with no address, where the file has none, is refused", async () => {
    const def = await load();
    const host = network([], settingsFor(file(device({ ip: undefined })), "plug=@3.3"));
    expect(() => def.devices.poll(host.ctx)).toThrow("host plug needs an address and a version, and the devices file has none");
  });

  it("a host with no version, where the file has none, is refused", async () => {
    const def = await load();
    const host = network([], settingsFor(file(device({ version: undefined })), `plug=${IP}`));
    expect(() => def.devices.poll(host.ctx)).toThrow("host plug needs an address and a version, and the devices file has none");
  });

  it("a host given by id alone takes its address and version from the file, and empty entries are skipped", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.4", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device({ version: "3.4" })), ` ,plug, `));
    run(def, host, 2);
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: true });
  });

  it("refuses a devices file that isn't a list, and one that isn't JSON", async () => {
    const def = await load();
    expect(() => def.devices.poll(network([], settingsFor('{"a":1}', `plug=${IP}@3.3`)).ctx)).toThrow("the devices file must hold a list of devices");
    const other = await load();
    expect(() => other.devices.poll(network([], settingsFor("not json", `plug=${IP}@3.3`)).ctx)).toThrow(SyntaxError);
  });

  it("skips entries with no id, and names a device by its product or its id when it has no name", async () => {
    const def = await load();
    const host = network([], settingsFor(file(null, { name: "no id" }, device({ id: "", name: "blank" }), device({ id: "named", name: "", product_name: "Socket" }), device({ id: "bare", name: "", product_name: "" })), `named=${IP}@3.3,bare=${IP}@3.3`));
    expect(def.devices.poll(host.ctx).map((c) => [c.id, c.name])).toEqual([
      ["named", "Socket"],
      ["bare", "bare"],
    ]);
  });
});

describe("tuya: what devices.poll reports", () => {
  it("declares each value in data point order, with its type, writability, range, unit and scale", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.4", dps: { "cid-th": { "1": true, "2": 210, "3": 205, "4": "auto", "6": 7, "7": { a: 1 }, "8": 2, "10": false } } });
    const host = network([dev], settingsFor(file(...gatewayFile()), `gw1=${IP}@3.4`));
    run(def, host, 6);

    const th = byId(def.devices.poll(host.ctx), "th1");
    expect(th.kind).toBe("climate");
    expect(th.values.map((v: DeviceValue) => v.code)).toEqual(["switch", "c_f", "heating_state", "temp_set", "temp_current", "mode", "factory_reset", "add_ele", "schedule", "fault"]);
    const values = valuesByCode(th);
    expect(values.switch).toMatchObject({ type: "Boolean", writable: true, value: true });
    expect(values.temp_set).toMatchObject({ type: "Integer", writable: true, value: 210, min: 50, max: 300, step: 5, unit: "℃", scale: 1 });
    expect(values.temp_current).toMatchObject({ type: "Integer", writable: false, value: 205, scale: 1 });
    expect(values.mode).toMatchObject({ type: "Enum", writable: true, value: "auto", range: ["auto", "manual"] });
    expect(values.factory_reset).toMatchObject({ type: "Boolean", writable: false, value: null });
    expect(values.add_ele).toMatchObject({ type: "Integer", unit: "kWh", writable: false, value: 7 });
    expect(values.schedule).toMatchObject({ type: "String", value: '{"a":1}' });
    expect(values.fault).toMatchObject({ type: "String", value: "2" });
    expect(values.c_f).toMatchObject({ type: "Enum", value: "c", writable: false });
    expect(values.heating_state).toMatchObject({ type: "Boolean", value: null, writable: false });
  });

  it("writes only what the device's current type allows: a boolean for a switch, a number for a value, a text for a choice", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": "yes", "2": "hot", "3": 5 } } });
    const plugMapping = {
      "1": { code: "switch_1", type: "Boolean", values: {} },
      "2": { code: "speed", type: "Enum", values: { range: ["low", "high"] } },
      "3": { code: "level", type: "Integer", values: { min: 0, max: 9, step: 1 } },
    };
    const host = network([dev], settingsFor(file(device({ mapping: plugMapping })), `plug=${IP}@3.3`));
    run(def, host, 2);
    expect(valuesByCode(def.devices.poll(host.ctx)[0])).toMatchObject({
      switch_1: { writable: false, value: "yes" },
      speed: { writable: true, value: "hot" }, // an Enum takes any text, so this one is writable whatever its choices
      level: { writable: true, value: 5, min: 0, max: 9, step: 1 },
    });
  });

  it("shows an unknown value as unknown, and a boolean choice with three options as the boolean it is", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "5": true } } });
    const mapping = {
      "1": { code: "switch_1", type: "Boolean", values: {} },
      "5": { code: "speed", type: "Enum", values: { range: ["a", "b", "c"] } },
    };
    const host = network([dev], settingsFor(file(device({ mapping })), `plug=${IP}@3.3`));
    run(def, host, 2);
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.switch_1).toMatchObject({ value: null, writable: true });
    expect(values.speed).toMatchObject({ value: true, writable: false });
  });

  it("names a bitmap's set bits, shows a number a bit doesn't name, and keeps any other value as it's sent", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": 5, "2": 0, "3": 9, "4": "text" } } });
    const mapping = {
      "1": { code: "fault", type: "Bitmap", values: { label: ["a", "b", "c"] } },
      "2": { code: "fault_none", type: "Bitmap", values: { label: ["a", "b"] } },
      "3": { code: "fault_big", type: "Bitmap", values: { label: ["a", "b", "c"] } },
      "4": { code: "blob", type: "Raw", values: "{not json" },
    };
    const host = network([dev], settingsFor(file(device({ mapping })), `plug=${IP}@3.3`));
    run(def, host, 2);
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.fault.value).toBe("a, c");
    expect(values.fault_none.value).toBe("0");
    expect(values.fault_big.value).toBe("9");
    expect(values.blob).toMatchObject({ type: "String", value: "text" });
  });

  it("gives duplicate codes a suffix, skips entries that aren't mappings, and reads a category it doesn't know as other", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true, "2": false } } });
    const mapping = {
      "1": { code: "switch_1", type: "Boolean", values: {} },
      "2": { code: "switch_1", type: "Boolean", values: {} },
      "3": null,
    };
    const host = network([dev], settingsFor(file(device({ category: "zz", mapping })), `plug=${IP}@3.3`));
    run(def, host, 2);
    const card = def.devices.poll(host.ctx)[0];
    expect(card.kind).toBe("other");
    expect(card.values.map((v: DeviceValue) => v.code)).toEqual(["switch_1", "switch_1_2"]);
  });

  it("scales a mains voltage only when its limits say it's tenths, and reads a reading's unit", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "6": 2340, "7": 2340 } } });
    const mapping = {
      "6": { code: "cur_voltage", type: "Integer", values: { min: 0, max: 2500, unit: "V" } },
      "7": { code: "cur_current", type: "Integer", values: { min: 0, max: 500, unit: "A", scale: 3 } },
    };
    const host = network([dev], settingsFor(file(device({ mapping })), `plug=${IP}@3.3`));
    run(def, host, 2);
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.cur_voltage).toMatchObject({ scale: 1, unit: "V", value: 2340 });
    expect(values.cur_current).toMatchObject({ scale: 3, unit: "A" });
  });

  it("reads a light's colour as words, offers its parts as numbers, and sends a change in the form the light reported", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "white", "3": "00dc000003e8" } } });
    const host = network([dev], settingsFor(file(lamp(COLOUR_MAPPING)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    const card = def.devices.poll(host.ctx)[0];
    expect(card.values.map((v: DeviceValue) => v.code)).toEqual(["work_mode", "colour_data", "colour_hue", "colour_saturation", "colour_brightness"]);
    const values = valuesByCode(card);
    expect(values.colour_data.value).toBe("hue 220, saturation 0%, brightness 100%");
    expect(values.colour_hue).toMatchObject({ type: "Integer", writable: true, value: 220, min: 0, max: 360 });
    expect(values.colour_saturation).toMatchObject({ writable: true, value: 0, min: 0, max: 1000 });
    expect(values.colour_brightness).toMatchObject({ writable: true, value: 1000, min: 0, max: 1000 });
  });

  it("takes a light's own top for saturation when its mapping states one, and shows its parts unknown until it reports a colour", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "white" } } });
    const mapping = { ...COLOUR_MAPPING, "3": { code: "colour_data", type: "Json", values: { s: { max: 100 } } } };
    const host = network([dev], settingsFor(file(lamp(mapping)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    const values = valuesByCode(def.devices.poll(host.ctx)[0]);
    expect(values.colour_hue).toMatchObject({ value: null, writable: false, min: 0, max: 360 });
    expect(values.colour_saturation).toMatchObject({ value: null, writable: false, max: 1000 });
    expect(values.colour_data.value).toBeNull();
  });
});

describe("tuya: devices.write", () => {
  it("refuses a device it doesn't know, a value it doesn't have, one that isn't writable, and one whose device isn't connected", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true, "4": 150 } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.3`));
    run(def, host, 2);
    expect(() => def.devices.write(host.ctx, "nobody", "switch_1", true)).toThrow("unknown device nobody");
    expect(() => def.devices.write(host.ctx, "plug", "nope", true)).toThrow("Plug has no value called nope");
    expect(() => def.devices.write(host.ctx, "plug", "cur_current", 1)).toThrow("cur_current is not writable");

    // A fresh load, since the links are read once per load: this one has the plug with no device behind it.
    const other = await load();
    const away = network([], settingsFor(file(device()), `plug=${IP}@3.3`));
    expect(() => other.devices.write(away.ctx, "plug", "switch_1", true)).toThrow("Plug isn't connected");
  });

  it("sends a change to a device's own data point, in the form its protocol uses", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.3`));
    run(def, host, 2);
    vi.setSystemTime(NOW);
    def.devices.write(host.ctx, "plug", "switch_1", false);
    expect(dev.controls).toEqual([{ cmd: 7, json: { devId: "plug", uid: "plug", t: "1700000000", dps: { "1": false } } }]);
  });

  it("refuses a change to a value that isn't writable for its type, such as a JSON value", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true, "7": { a: 1 } } } });
    const mapping = { "1": { code: "switch_1", type: "Boolean", values: {} }, "7": { code: "timer", type: "Json", values: "" } };
    const host = network([dev], settingsFor(file(device({ mapping })), `plug=${IP}@3.3`));
    run(def, host, 2);
    expect(() => def.devices.write(host.ctx, "plug", "timer", "x")).toThrow("timer is not writable");
  });

  it("a send that fails takes the link down, logs why, and throws", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.3`));
    run(def, host, 2);
    dev.last.close();
    expect(() => def.devices.write(host.ctx, "plug", "switch_1", false)).toThrow("use of closed network connection");
    expect(host.logs.at(-1)).toBe("Plug: link down, reconnecting: Error: use of closed network connection");
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: false });
  });

  it("a colour part is written in the form the light last sent, and turns on colour mode", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "white", "3": "00dc000003e8" } } });
    const host = network([dev], settingsFor(file(lamp(COLOUR_MAPPING)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    vi.setSystemTime(NOW);
    def.devices.write(host.ctx, "lamp", "colour_hue", 120);
    expect(dev.controls[0].json).toEqual({ devId: "lamp", uid: "lamp", t: "1700000000", dps: { "3": "0078000003e8", "2": "colour" } });
  });

  it("a saturation written straight after a hue keeps the new hue, before the light reports it", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "colour", "3": "00dc000003e8" } } });
    const host = network([dev], settingsFor(file(lamp(COLOUR_MAPPING)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    def.devices.write(host.ctx, "lamp", "colour_hue", 0);
    def.devices.write(host.ctx, "lamp", "colour_saturation", 831);
    expect(dev.controls[1].json.dps).toEqual({ "3": "0000033f03e8", "2": "colour" });
  });

  it("a colour part of an RGB light is sent as RGB, with its hue, saturation and value", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "colour", "3": "ff00000000ffff" } } });
    const host = network([dev], settingsFor(file(lamp(COLOUR_MAPPING)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    vi.setSystemTime(NOW);
    def.devices.write(host.ctx, "lamp", "colour_hue", 120);
    expect(dev.controls[0].json.dps).toEqual({ "3": "00ff000078ffff", "2": "colour" });
  });

  it("a colour part of a light that sends JSON is sent as JSON, and only where the light has no colour mode to turn on", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "3": '{"h":10,"s":500,"v":500}' } } });
    const host = network([dev], settingsFor(file(lamp({ "3": { code: "colour_data", type: "Json", values: {} } })), `lamp=${IP}@3.3`));
    run(def, host, 2);
    vi.setSystemTime(NOW);
    def.devices.write(host.ctx, "lamp", "colour_brightness", 800);
    expect(dev.controls[0].json.dps).toEqual({ "3": '{"h":10,"s":500,"v":800}' });
  });

  it("a colour part of a light whose mode list has no colour leaves the mode alone", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "white", "3": { h: 10, s: 500, v: 500 } } } });
    const mapping = { "2": { code: "work_mode", type: "Enum", values: { range: ["white", "scene"] } }, "3": { code: "colour_data", type: "Json", values: {} } };
    const host = network([dev], settingsFor(file(lamp(mapping)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    vi.setSystemTime(NOW);
    def.devices.write(host.ctx, "lamp", "colour_saturation", 250);
    expect(dev.controls[0].json.dps).toEqual({ "3": { h: 10, s: 250, v: 500 } });
  });

  it("refuses a colour part until the light has reported its colour", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "2": "white" } } });
    const host = network([dev], settingsFor(file(lamp(COLOUR_MAPPING)), `lamp=${IP}@3.3`));
    run(def, host, 2);
    expect(() => def.devices.write(host.ctx, "lamp", "colour_hue", 120)).toThrow("Lamp hasn't reported its colour yet");
  });
});
