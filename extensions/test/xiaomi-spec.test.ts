import { describe, expect, it, vi } from "vitest";
import { makeHost, TIMEOUT, valuesByCode } from "./harness";
import { load, HELLO, IP, SPEC_BASE, BULB_URN, Miio, properties, network, devicesFile, device, LAMP_VALUES } from "./xiaomi-fake";

describe("xiaomi: a model known only by its spec", () => {
  const specDoc = (extra: object[] = []) => ({
    services: [
      {
        iid: 2,
        type: "urn:miot-spec-v2:service:light:00007802:acme-bulb:1",
        description: "Light",
        properties: [
          { iid: 1, type: "urn:miot-spec-v2:property:on:00000006:acme-bulb:1", format: "bool", access: ["read", "write", "notify"], description: "On" },
          { iid: 2, type: "urn:miot-spec-v2:property:brightness:0000000D:acme-bulb:1", format: "uint8", access: ["read", "write"], unit: "percentage", "value-range": [1, 100, 1] },
          { iid: 3, type: "urn:miot-spec-v2:property:color-temperature:0000000A:acme-bulb:1", format: "uint32", access: ["read", "write"], unit: "kelvin", "value-range": [2700, 6500, 100] },
          { iid: 4, type: "urn:miot-spec-v2:property:mode:00000008:acme-bulb:1", format: "uint8", access: ["read", "write"], "value-list": [{ value: 0, description: "Day" }, { value: 1, description: "Night" }] },
          { iid: 5, type: "urn:miot-spec-v2:property:temperature:00000011:acme-bulb:1", format: "float", access: ["read"], unit: "celsius" },
          { iid: 6, type: "urn:miot-spec-v2:property:status-text:00000016:acme-bulb:1", format: "string", access: ["read"] },
        ],
        actions: [],
      },
      ...extra,
    ],
  });
  const bulbFile = (over: Record<string, unknown> = {}) => devicesFile(device({ model: "acme.light.bulb", spec_type: BULB_URN, ...over }));
  const bulbValues = { "2.1": true, "2.2": 80, "2.3": 4000, "2.4": 1, "2.5": 21.456, "2.6": "SN1" };

  it("fetches the spec once, then reads the device with it", async () => {
    const def = await load();
    const bulb = new Miio(IP, 1234567, properties(bulbValues));
    const host = network([bulb], {
      settings: { devices: bulbFile() },
      fetch: (url) => (url === SPEC_BASE + BULB_URN ? { body: specDoc() } : { status: 404 }),
    });
    const [card] = def.devices.poll(host.ctx);
    const values = valuesByCode(card);

    expect(host.fetches.map((f) => f.url)).toEqual([SPEC_BASE + BULB_URN]);
    expect(card).toMatchObject({ kind: "light", online: true });
    expect(values.power).toMatchObject({ type: "Boolean", writable: true, value: true });
    expect(values.brightness).toMatchObject({ type: "Integer", writable: true, value: 80, min: 1, max: 100, unit: "%" });
    expect(values.color_temperature).toMatchObject({ type: "Integer", writable: true, value: 4000, min: 2700, max: 6500, step: 100, unit: "K" });
    expect(values.mode).toMatchObject({ type: "Enum", writable: true, value: "night", range: ["day", "night"] });
    expect(values.temperature).toMatchObject({ type: "Number", writable: false, value: 21.456, unit: "°C" });
    expect(values.status_text).toMatchObject({ type: "String", writable: false, value: "SN1" });
    // The first read asks for one property, to find out whether the device speaks MIoT at all.
    expect(bulb.methods("get_properties")[0].params).toHaveLength(1);
  });

  it("writes through the spec's own property ids, with the names it gave", async () => {
    const def = await load();
    const bulb = new Miio(IP, 1234567, (method, params) => {
      if (method === "set_properties") return { result: params.map((p: any) => ({ code: 0 })) };
      return properties(bulbValues)(method, params);
    });
    const host = network([bulb], { settings: { devices: bulbFile() }, fetch: () => ({ body: specDoc() }) });
    def.devices.poll(host.ctx);
    def.devices.write(host.ctx, "1234567", "brightness", 55);
    def.devices.write(host.ctx, "1234567", "mode", "day");
    expect(bulb.methods("set_properties").map((c) => c.params[0])).toEqual([
      { did: "2.2", siid: 2, piid: 2, value: 55 },
      { did: "2.4", siid: 2, piid: 4, value: 0 },
    ]);
    expect(() => def.devices.write(host.ctx, "1234567", "temperature", 3)).toThrow("temperature is not writable");
  });

  it("keeps the device as a card that says it's waiting while its spec can't be fetched", async () => {
    const def = await load();
    const bulb = new Miio(IP, 1234567, properties(bulbValues));
    const host = network([bulb], {
      settings: { devices: bulbFile() },
      fetch: () => {
        throw new Error("network down");
      },
    });
    const [card] = def.devices.poll(host.ctx);
    expect(card.online).toBe(false);
    expect(valuesByCode(card).unavailable.value).toBe("Waiting for its description from miot-spec.org. It's tried again on its own.");
    expect(host.logs.some((l) => l.includes("no description yet for a device"))).toBe(true);
    expect(bulb.calls).toHaveLength(0);
  });

  it("says when miot-spec.org has no such model, and when the reply isn't a spec", async () => {
    const def = await load();
    const missing = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() }, fetch: () => ({ status: 404 }) });
    const [gone] = def.devices.poll(missing.ctx);
    expect(valuesByCode(gone).unavailable.value).toMatch(/has no description of this model/);

    const fresh = await load(); // the missing model is remembered by the module, so this is a new run
    const notSpec = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() }, fetch: () => ({ body: { nope: 1 } }) });
    fresh.devices.poll(notSpec.ctx);
    expect(notSpec.logs.some((l) => l.includes("that isn't a spec"))).toBe(true);
  });

  it("waits a minute before asking again after a failure, and not at all for a missing model until hours have passed", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(10_000_000);
    const def = await load();
    const host = network([new Miio(IP, 1234567, properties(bulbValues))], {
      settings: { devices: bulbFile() },
      fetch: () => {
        throw new Error("down");
      },
    });
    def.devices.poll(host.ctx);
    def.devices.poll(host.ctx); // paused: no request
    expect(host.fetches).toHaveLength(1);
    vi.setSystemTime(10_000_000 + 61_000);
    def.devices.poll(host.ctx);
    expect(host.fetches).toHaveLength(2);
  });

  it("shows a device that answers no MIoT calls as an older one, or as one it can't read", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(10_000_000);
    const def = await load();
    const refusing = new Miio(IP, 1234567, (method) => (method === "get_properties" ? { error: { code: -32601, message: "method not found" } } : undefined));
    const host = network([refusing], { settings: { devices: bulbFile() }, fetch: () => ({ body: specDoc() }) });
    const [card] = def.devices.poll(host.ctx);
    expect(host.logs.some((l) => l.includes("doesn't take MIoT reads"))).toBe(true);
    expect(card.online).toBe(false);
  });

  it("gives an empty description a note of its own", async () => {
    const def = await load();
    const empty = { services: [{ iid: 2, type: "urn:miot-spec-v2:service:light:00007802:acme-bulb:1", description: "Light", properties: [], actions: [] }] };
    const host = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() }, fetch: () => ({ body: empty }) });
    const [card] = def.devices.poll(host.ctx);
    expect(valuesByCode(card).unavailable.value).toContain("Many of these are Bluetooth devices");
  });

  it("restores a spec saved earlier without asking the server again", async () => {
    const first = await load();
    const bulb = new Miio(IP, 1234567, properties(bulbValues));
    const host = network([bulb], { settings: { devices: bulbFile() }, fetch: () => ({ body: specDoc() }) });
    first.devices.poll(host.ctx);
    expect(host.fetches).toHaveLength(1);

    // A restart: the extension's memory is gone, but the owner's storage keeps the spec.
    const second = await load();
    const restarted = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() }, fetch: () => ({ body: specDoc() }) });
    restarted.storage.clear();
    for (const [k, v] of host.storage) restarted.storage.set(k, v);
    const [card] = second.devices.poll(restarted.ctx);
    expect(card.online).toBe(true);
    expect(restarted.fetches).toHaveLength(0);
  });

  it("logs a saved spec it can't use, and goes on", async () => {
    const def = await load();
    const host = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() } });
    host.storage.set("spec1:" + BULB_URN, [{ iid: 2, name: "light", props: null, actions: [] }]);
    host.storage.set("spec1-index", []);
    const [card] = def.devices.poll(host.ctx);
    expect(card.online).toBe(false);
    expect(host.logs.some((l) => l.includes("a bad description"))).toBe(true);
  });

  it("keeps the spec's first services and drops ones it can't use", async () => {
    const def = await load();
    const extra = { iid: 3, type: "urn:miot-spec-v2:service:battery:00007806:acme-bulb:1", properties: [{ iid: 1, type: "urn:miot-spec-v2:property:battery-level:0000000E:acme-bulb:1", format: "uint8", access: ["read"] }], actions: [] };
    const host = network([new Miio(IP, 1234567, properties({ "2.1": true, "3.1": 90 }))], {
      settings: { devices: bulbFile() },
      fetch: () => ({ body: specDoc([extra, { iid: 4, type: "urn:miot-spec-v2:service:firmware:00007807:acme-bulb:1", properties: [{ iid: 1, type: "urn:miot-spec-v2:property:firmware-version:00000025:acme-bulb:1", format: "string", access: ["read"] }], actions: [] }]) }),
    });
    const [card] = def.devices.poll(host.ctx);
    const values = valuesByCode(card);
    expect(values.battery).toMatchObject({ value: 90, type: "Integer" });
    expect(values.firmware_version).toBeUndefined(); // firmware is never shown
  });

  it("keeps each saved spec, and drops the oldest once they fill the store", async () => {
    const def = await load();
    const hugeProps = (n: number) =>
      Array.from({ length: 64 }, (_, i) => ({
        iid: i + 1,
        type: `urn:miot-spec-v2:property:mode-${n}-${i}:00000008:acme-bulb:1`,
        format: "uint8",
        access: ["read", "write"],
        "value-list": Array.from({ length: 64 }, (_, v) => ({ value: v, description: `choice ${v} of setting ${i} in model ${n} xx`.slice(0, 40) })),
      }));
    const devices = [1, 2, 3, 4, 5].map((n) => device({ did: String(1000000 + n), name: `Model ${n}`, model: `acme.light.big${n}`, localip: `192.168.1.${70 + n}`, spec_type: `urn:miot-spec-v2:device:light:0000A001:acme-big${n}:1` }));
    const specs = Object.fromEntries(devices.map((d) => [d.spec_type as string, { services: [{ iid: 2, type: "urn:miot-spec-v2:service:light:00007802:x:1", properties: hugeProps(Number(String(d.did).slice(-1))), actions: [] }] }]));
    const miios = devices.map((d) => new Miio(String(d.localip), Number(d.did), () => undefined));
    const host = network(miios, { settings: { devices: devicesFile(...devices) }, fetch: (url) => ({ body: specs[url.slice(SPEC_BASE.length)] }) });
    def.devices.poll(host.ctx); // three specs
    def.devices.poll(host.ctx); // the other two, and the store makes room
    expect(host.fetches).toHaveLength(5);
    const index = host.storage.get("spec1-index") as [string, number][];
    expect(index.length).toBeLessThan(5);
    expect(host.storage.has("spec1:" + devices[0].spec_type)).toBe(false);
  });

  it("keeps a spec that's too large to save for this session only", async () => {
    const def = await load();
    const body = { services: [{ iid: 2, type: "urn:miot-spec-v2:service:light:00007802:x:1", properties: Array.from({ length: 160 }, (_, i) => ({ iid: i + 1, type: `urn:miot-spec-v2:property:p${i}:00000008:x:1`, format: "uint8", access: ["read"], "value-list": Array.from({ length: 64 }, (_, v) => ({ value: v, description: `${"x".repeat(40)}${v}` })) })), actions: [] }] };
    const host = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() }, fetch: () => ({ body }) });
    def.devices.poll(host.ctx);
    expect(host.storage.size).toBe(0); // over half the store, so not kept, and the spec stays in memory for this run
  });

  it("logs a spec it couldn't store, and carries on", async () => {
    const def = await load();
    const host = network([new Miio(IP, 1234567, properties(bulbValues))], { settings: { devices: bulbFile() }, fetch: () => ({ body: specDoc() }) });
    (host.ctx.storage as any).set = () => {
      throw new Error("storage refused");
    };
    const [card] = def.devices.poll(host.ctx);
    expect(card.online).toBe(true);
    expect(host.logs.some((l) => l.includes("couldn't save a device description"))).toBe(true);
  });

  it("drops the values a device can't fit, when every device together is over the list's limit", async () => {
    const def = await load();
    const count = 6;
    const big = Array.from({ length: count }, (_, k) => device({ did: String(2000000 + k), name: `Big ${k}`, model: `acme.light.huge${k}`, localip: `192.168.1.${100 + k}`, spec_type: `urn:miot-spec-v2:device:light:0000A001:acme-huge${k}:1` }));
    const props = (k: number) =>
      Array.from({ length: 64 }, (_, i) => ({
        iid: i + 1,
        type: `urn:miot-spec-v2:property:setting-${k}-${i}:00000008:x:1`,
        format: "uint8",
        access: ["read", "write"],
        "value-list": Array.from({ length: 64 }, (_, v) => ({ value: v, description: `option ${v} for setting ${i} of device ${k} ${"-".repeat(20)}`.slice(0, 40) })),
      }));
    const specs = Object.fromEntries(big.map((d, k) => [d.spec_type as string, { services: [{ iid: 2, type: "urn:miot-spec-v2:service:light:00007802:x:1", properties: props(k), actions: [] }] }]));
    const miios = big.map((d) => new Miio(String(d.localip), Number(d.did), () => undefined));
    const host = network(miios, { settings: { devices: devicesFile(...big) }, fetch: (url) => ({ body: specs[url.slice(SPEC_BASE.length)] }) });
    def.devices.poll(host.ctx);
    const cards = def.devices.poll(host.ctx);
    const total = JSON.stringify(cards).length;
    expect(total).toBeLessThanOrEqual(1024 * 1024);
    expect(cards.some((c: ExtensionDevice) => c.values.some((v: DeviceValue) => v.value === "This home has more device values than one list can hold, so this device's are left out."))).toBe(true);
  });
});

describe("xiaomi: the devices file", () => {
  it("refuses a file that isn't JSON, or isn't a list", async () => {
    const def = await load();
    const broken = makeHost({ settings: { devices: "{nope" } });
    expect(() => def.devices.poll(broken.ctx)).toThrow("the devices file isn't valid JSON");
    const notList = makeHost({ settings: { devices: '{"homes":[]}' } });
    expect(() => def.devices.poll(notList.ctx)).toThrow("the devices file should be a list");
  });

  it("refuses a table device whose token isn't hex", async () => {
    const def = await load();
    const host = makeHost({ settings: { devices: devicesFile(device({ token: "not-hex!" })) } });
    expect(() => def.devices.poll(host.ctx)).toThrow("the token for Desk lamp isn't hex");
  });

  it("skips an entry it can't read, and a device listed twice", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const file = JSON.stringify([
      null,
      { homes: [null, { devices: [null, { did: {}, name: "x" }, device({ name: 5 }), device(), device({ name: "again" })] }] },
    ]);
    const host = network([lamp], { settings: { devices: file } });
    const cards = def.devices.poll(host.ctx);
    expect(cards.map((c: ExtensionDevice) => c.name)).toEqual(["Desk lamp"]);
    expect(host.logs.filter((l) => l.includes("skipping an unreadable device entry"))).toHaveLength(3);
  });

  it("names a device by its model when it has no name", async () => {
    const def = await load();
    const host = network([new Miio(IP, 1234567, properties(LAMP_VALUES))], { settings: { devices: devicesFile(device({ name: undefined, model: "" })) } });
    const [card] = def.devices.poll(host.ctx);
    expect(card.name).toBe("1234567");
  });

  it("explains each device it can't reach over the LAN, and never polls it", async () => {
    const def = await load();
    const file = devicesFile(
      device({ did: "blt.3.abc", name: "Mesh" }),
      device({ did: "lumi.1234", name: "Sensor" }),
      device({ did: "group.1", name: "Group" }),
      device({ did: "xyz", name: "Odd" }),
      device({ did: "3000", name: "No address", localip: "" }),
      device({ did: "3001", name: "Public", localip: "8.8.8.8", model: "acme.plug.one" }),
      device({ did: "3002", name: "Bad token", localip: IP, token: "zz", model: "acme.plug.two" }),
    );
    const host = network([], { settings: { devices: file } });
    const cards = def.devices.poll(host.ctx);
    expect(host.udpRequests).toHaveLength(0);
    const notes = Object.fromEntries(cards.map((c: ExtensionDevice) => [c.name, valuesByCode(c).unavailable.value]));
    expect(notes.Mesh).toMatch(/^Bluetooth or Mesh/);
    expect(notes.Sensor).toMatch(/^Zigbee/);
    expect(notes.Group).toBe("A group made in Mi Home, not a device.");
    expect(notes.Odd).toMatch(/^Not a Wi-Fi device/);
    expect(notes["No address"]).toMatch(/no local address or token/);
    expect(notes.Public).toBe("The devices file gives it an address outside your local network.");
    expect(notes["Bad token"]).toBe("Its token in the devices file isn't hex.");
    expect(cards.every((c: ExtensionDevice) => c.online === false)).toBe(true);
  });

  it("notes a device whose spec type and model are both unknown", async () => {
    const def = await load();
    const host = network([], { settings: { devices: devicesFile(device({ model: "acme.thing.x", localip: "" })) } });
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).unavailable.value).toMatch(/no local address or token/);
  });

  it("notes a device with neither a spec type nor a known model", async () => {
    const def = await load();
    const host = network([new Miio(IP, 1234567, properties(LAMP_VALUES))], { settings: { devices: devicesFile(device({ model: "acme.thing.y" })) } });
    const [card] = def.devices.poll(host.ctx);
    expect(valuesByCode(card).unavailable.value).toMatch(/isn't a known older model/);
  });

  it("shows a discovered device, or one it can't name, from the LAN hello", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const stranger = new Miio("192.168.1.61", 7654321, properties({}));
    const host = makeHost({
      settings: { devices: devicesFile(device()) },
      udpScan: (hosts, _port, data) => [
        ...[lamp, stranger].flatMap((d) => (hosts.includes(d.ip) && d.reply(data) !== null ? [{ from: d.ip, data: d.reply(data)! }] : [])),
        { from: "192.168.1.62", data: "zz" },
      ],
    });
    expect(def.devices.discover(host.ctx)).toEqual([
      { id: "1234567", name: "Desk lamp", product: "yeelink.light.ceiling5", address: IP },
      { id: "7654321", name: "Unknown Xiaomi device", address: "192.168.1.61", note: "It isn't in your devices file, so its token isn't known. Pair it in Mi Home if it isn't yet, then fetch the file again with Xiaomi-cloud-tokens-extractor." },
    ]);
  });

  it("scans every address on each /24 its devices are on, and not public ones", async () => {
    const def = await load();
    const host = makeHost({ settings: { devices: devicesFile(device(), device({ did: "9", localip: "10.0.0.5", name: "Far" }), device({ did: "8", localip: "8.8.4.4", name: "Out" })) } });
    def.devices.discover(host.ctx);
    expect(host.udpScans.map((s) => s.host.split(",").length)).toEqual([254, 254]);
    expect(host.udpScans[0].data).toBe(HELLO);
  });

  it("finds nothing when no device in the file is on a private address", async () => {
    const def = await load();
    const host = makeHost({ settings: { devices: devicesFile(device({ localip: "8.8.8.8" })) } });
    expect(def.devices.discover(host.ctx)).toEqual([]);
    expect(host.udpScans).toHaveLength(0);
  });
});

describe("xiaomi: the connection", () => {
  it("drops a device that is answered by the wrong key, and reports it offline", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    lamp.corrupt = true;
    const host = network([lamp], { settings: { devices: devicesFile(device()) } });
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
  });

  it("uses the device's own key for packets: a request under another token gets no answer", async () => {
    const def = await load();
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = network([lamp], { settings: { devices: devicesFile(device({ token: "ff".repeat(16) })) } });
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(lamp.calls).toHaveLength(0);
  });

  it("reports the transport's own refusal as the reason a device is offline", async () => {
    const def = await load();
    let down = false;
    const lamp = new Miio(IP, 1234567, properties(LAMP_VALUES));
    const host = makeHost({
      settings: { devices: devicesFile(device()) },
      udpRequest: (ip, _port, data) => {
        if (down) throw new Error("write udp: no route to host");
        const reply = lamp.reply(data);
        if (reply === null) throw new Error(TIMEOUT);
        return reply;
      },
    });
    def.devices.poll(host.ctx);
    down = true;
    const [card] = def.devices.poll(host.ctx);
    expect(card.online).toBe(false);
    expect(host.logs.join(" ")).toMatch(/no route to host/);
  });
});
