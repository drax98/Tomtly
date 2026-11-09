import { describe, expect, it, vi } from "vitest";
import { valuesByCode } from "./harness";

// The Tuya extension, against a fake device on each protocol. The fake mirrors internal/extension/tuya_ext_test.go (3.4 and
// 3.5) and tuya_crc_fake_test.go (3.1 to 3.3), but it's driven in-process: the extension writes a frame, the connection's
// write() hands it to the device, and the device's answer is queued for the next read.
import { load, KEY, OTHER, IP, NOW, SEC, bytesOf, hasSession, crcFrame, sessionFrame, TuyaDevice, network, device, problems, file, settingsFor, run, PROTOCOLS, gatewayFile, byId, passTime } from "./tuya-fake";

describe("tuya: a gateway and its children", () => {
  it.each(PROTOCOLS.slice(1))("%s: children are read by cid, and a child that never reports stays offline with no value", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "cid-th": { "2": 210 } } });
    const host = network([dev], settingsFor(file(...gatewayFile()), `gw1=${IP}@${v}`));
    run(def, host, 6);

    const cards = def.devices.poll(host.ctx);
    expect(byId(cards, "gw1")).toMatchObject({ online: true, name: "Gateway" });
    expect(byId(cards, "th1")).toMatchObject({ online: true, kind: "climate" });
    expect(valuesByCode(byId(cards, "th1")).temp_set.value).toBe(210);
    expect(byId(cards, "door1")).toMatchObject({ online: false });
    expect(valuesByCode(byId(cards, "door1")).open.value).toBeNull();
    expect(dev.reads.map((r) => r?.cid).filter(Boolean)).toEqual(expect.arrayContaining(["cid-th", "cid-door"]));
  });

  it.each(PROTOCOLS.slice(1))("%s: a write to a child goes out on the gateway, with the child's cid", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "cid-th": { "2": 210 } } });
    const host = network([dev], settingsFor(file(...gatewayFile()), `gw1=${IP}@${v}`));
    run(def, host, 3);

    vi.setSystemTime(NOW); // the reads above ran the clock on, and the control's time is the fixed one
    def.devices.write(host.ctx, "th1", "temp_set", 225);
    const expected = hasSession(v)
      ? { protocol: 5, t: SEC, cid: "cid-th", data: { dps: { "2": 225 }, cid: "cid-th", ctype: 0 } }
      : { t: SEC, cid: "cid-th", dps: { "2": 225 } };
    expect(dev.controls).toEqual([{ cmd: hasSession(v) ? 0x0d : 7, json: expected }]);
  });

  it("3.3: a push with the cid at the top level is read like one with it inside data", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "cid-th": { "2": 210 } } });
    const host = network([dev], settingsFor(file(...gatewayFile()), `gw1=${IP}@3.3`));
    run(def, host, 3);
    dev.last.receive(dev.pushFrame(bytesOf(JSON.stringify({ cid: "cid-th", dps: { "2": 230 } }))).toString("hex"));
    run(def, host);
    expect(valuesByCode(byId(def.devices.poll(host.ctx), "th1")).temp_set.value).toBe(230);
  });

  it("a push from a child that isn't in the file is logged and dropped", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.4", dps: { "cid-th": { "2": 210 } } });
    const host = network([dev], settingsFor(file(...gatewayFile()), `gw1=${IP}@3.4`));
    run(def, host, 3);
    dev.push("cid-new", { "2": 1 });
    run(def, host);
    expect(problems(host)).toEqual(["Gateway: unknown sub-device cid-new"]);
  });

  it("a push from a child in the file is read by its cid", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.4", dps: { "cid-th": { "2": 210 } } });
    const host = network([dev], settingsFor(file(...gatewayFile()), `gw1=${IP}@3.4`));
    run(def, host, 3);
    dev.push("cid-door", { "101": true });
    run(def, host);
    expect(valuesByCode(byId(def.devices.poll(host.ctx), "door1")).open.value).toBe(true);
  });

  // Which gateway a child hangs off, from the file's own clues. The last column is the gateway it should land on.
  const assignments: [string, Record<string, unknown>, Record<string, unknown>, "a" | "b"][] = [
    ["by parent", { parent: "gwa" }, { parent: "gwb" }, "b"],
    ["by gateway_id", { gateway_id: "gwa" }, { gateway_id: "gwb" }, "b"],
    ["by a shared key", { key: KEY }, { key: OTHER }, "b"],
    ["with no clue, to the first gateway", { parent: "" }, { parent: "not-a-host" }, "a"],
  ];
  it.each(assignments)("two gateways, the child assigned %s", async (_name, clueA, clueB, landsOn) => {
    const def = await load();
    const a = new TuyaDevice(IP, { version: "3.4", dps: { "cid-a": { "1": true }, "cid-b": { "1": true } } });
    const b = new TuyaDevice("192.168.1.51", { version: "3.3", key: OTHER, dps: { "cid-a": { "1": true }, "cid-b": { "1": true } } });
    const children = [
      device({ id: "ca", name: "A", key: KEY, category: "cz", node_id: "cid-a", mapping: { "1": { code: "switch_1", type: "Boolean", values: {} } }, ...clueA }),
      device({ id: "cb", name: "B", key: "kkkkkkkkkkkkkkk2", category: "cz", node_id: "cid-b", mapping: { "1": { code: "switch_1", type: "Boolean", values: {} } }, ...clueB }),
    ];
    const gateways = [
      device({ id: "gwa", name: "GWA", category: "wg2", mapping: {}, version: "3.4" }),
      device({ id: "gwb", name: "GWB", key: OTHER, category: "wg2", mapping: {}, ip: "192.168.1.51", version: "3.3" }),
    ];
    const host = network([a, b], settingsFor(file(...gateways, ...children), `gwa=${IP}@3.4,gwb=192.168.1.51@3.3`));
    run(def, host, 4);
    def.devices.write(host.ctx, "cb", "switch_1", false);

    const reachedA = a.controls.some((c) => c.json.cid === "cid-b");
    const reachedB = b.controls.some((c) => c.json.cid === "cid-b");
    expect([reachedA, reachedB]).toEqual(landsOn === "b" ? [false, true] : [true, false]);
  });

  it("a child with no gateway at all is listed as unlinked, and no card is made for it", async () => {
    const def = await load();
    const host = network([], settingsFor(file(device({ id: "ch", node_id: "cid-x" })), ""));
    expect(def.devices.poll(host.ctx)).toEqual([]);
    expect(def.devices.discover(host.ctx)).toEqual([
      { id: "ch", name: "Plug", product: "Plug", note: expect.stringContaining("no entry in Hosts reaches it") },
    ]);
  });
});

describe("tuya: device22 and 'data unvalid'", () => {
  it("3.3: a device that answers 'data unvalid' is read with its data points as nulls, and then works", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", device22: true, dps: { "": { "1": true, "4": 150 } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.3`));
    run(def, host, 3);
    expect(dev.reads[0]).toMatchObject({ gwId: "plug", devId: "plug" });
    expect(dev.reads[1]).toMatchObject({ devId: "plug", uid: "plug", dps: { "1": null, "4": null } });
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: true });
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);
  });

  it("3.3: 'data unvalid' in clear proves nothing, so the device stays offline", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", device22: true, plainText: true, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.3`));
    run(def, host, 3);
    expect(dev.reads).toHaveLength(2);
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: false });
  });

  it("3.4: 'data unvalid' from a session device switches it to device22 reads", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.4", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.4`));
    run(def, host, 2);
    dev.last.receive(sessionFrame("3.4", dev.last.key, 8, bytesOf("json obj data unvalid")).toString("hex"));
    run(def, host, 2);
    expect(dev.controls.at(-1)).toEqual({ cmd: 0x0d, json: { devId: "plug", uid: "plug", t: expect.any(String), dps: { "1": null, "4": null } } });
  });
});

describe("tuya: the read budget", () => {
  it("spends at most 300 extra reads a minute on one link, and gets its reads back in the next minute", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.3`));
    run(def, host, 2);
    // Empty frames: they cost a read each and change nothing.
    for (let i = 0; i < 1000; i++) dev.last.receive(crcFrame(8, Buffer.alloc(0)).toString("hex"));

    const perRun: number[] = [];
    for (let i = 0; i < 70; i++) {
      const before = dev.last.readCalls.length;
      run(def, host);
      perRun.push(dev.last.readCalls.length - before);
    }
    // One read to start, five more each run. The status reply in setup spent one extra, so the 300 run out in the
    // 60th run: it gets four extras, then every run after it reads once.
    expect(perRun).toEqual([...Array(59).fill(6), 5, ...Array(10).fill(1)]);

    vi.setSystemTime(Date.now() + 60_000);
    const before = dev.last.readCalls.length;
    run(def, host);
    expect(dev.last.readCalls.length - before).toBe(6);
  });
});

describe("tuya: a link that goes quiet or never decrypts", () => {
  it.each(PROTOCOLS)("%s: a link quiet for thirty seconds is restarted, and the log says so", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);
    dev.mute = true;
    vi.setSystemTime(Date.now() + 31_000);
    run(def, host);
    expect(host.logs.at(-1)).toBe("Plug: link down, reconnecting: Error: no data for 30 seconds");
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
  });

  it.each(["3.2", "3.3"])("%s: a link that authenticates frames but never decrypts one is restarted after thirty seconds", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, key: OTHER, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    vi.setSystemTime(Date.now() + 31_000);
    // A frame with a valid CRC that holds nothing readable: it keeps the link from being silent, but proves nothing.
    dev.last.receive(crcFrame(8, Buffer.alloc(0)).toString("hex"));
    run(def, host);
    expect(host.logs.at(-1)).toBe("Plug: link down, reconnecting: Error: nothing readable for 30 seconds: is the local key or the version wrong?");
  });
});

describe("tuya: removed devices and the link limit", () => {
  it("when every device on a link is removed, the link is dropped and not dialled until one comes back", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.4", dps: { "": { "1": true } } });
    const removed: string[] = [];
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.4`), { removed });
    run(def, host, 2);
    expect(def.devices.poll(host.ctx)[0].online).toBe(true);

    removed.push("plug");
    run(def, host);
    expect(dev.last.closed).toBe(true);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    const dialled = dev.peers.length;
    run(def, host, 3);
    expect(dev.peers).toHaveLength(dialled);

    removed.length = 0;
    run(def, host, 2);
    expect(dev.peers).toHaveLength(dialled + 1);
    expect(def.devices.poll(host.ctx)[0].online).toBe(true);
  });

  it("only fifteen direct links fit, so a sixteenth device is logged as not connecting and is never dialled", async () => {
    const def = await load();
    const devices: TuyaDevice[] = [];
    const entries: Record<string, unknown>[] = [];
    const hosts: string[] = [];
    for (let i = 0; i < 16; i++) {
      const ip = `192.168.1.${10 + i}`;
      devices.push(new TuyaDevice(ip, { version: "3.3", dps: { "": { "1": true } } }));
      entries.push(device({ id: `d${i}`, name: `Plug ${i}`, ip }));
      hosts.push(`d${i}=${ip}@3.3`);
    }
    const host = network(devices, settingsFor(file(...entries), hosts.join(",")), { removed: ["d15"] });
    run(def, host, 3);
    expect(devices[14].peers).toHaveLength(1);
    expect(devices[15].peers).toHaveLength(0);
    expect(host.logs).toContain("Plug 15: only 15 direct connections fit, so this one won't connect. Devices behind a gateway don't count.");

    // Removed, then back: its link has no room, so it's still not dialled.
    run(def, host, 2);
    expect(devices[15].peers).toHaveLength(0);
    const cards = def.devices.poll(host.ctx);
    expect(byId(cards, "d14")).toMatchObject({ online: true });
    expect(byId(cards, "d15")).toMatchObject({ online: false });
  });
});

describe("tuya: a device that moved address", () => {
  it("is found again by probing its network, after a minute of failures, and then connects there", async () => {
    const def = await load();
    const moved = new TuyaDevice("192.168.1.77", { version: "3.3", dps: { "": { "1": true } } });
    const devices = [new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } })];
    const host = network(devices, settingsFor(file(device()), `plug=${IP}@3.3`), { tcpScan: (hosts) => hosts.filter((h) => devices.some((d) => d.ip === h)) });
    run(def, host, 2);
    devices.splice(0, devices.length, moved);

    vi.setSystemTime(Date.now() + 31_000); // the old address goes quiet, which starts the count
    run(def, host);
    passTime(def, host, 60);
    passTime(def, host, 90);
    run(def, host, 2);

    expect(host.logs).toContain("Plug found at a new address, 192.168.1.77, was 192.168.1.50");
    expect(host.tcpScans).toHaveLength(1);
    expect(host.tcpScans[0].hosts).not.toContain(IP);
    expect(moved.peers.length).toBeGreaterThan(0);
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: true });
  });

  it("a device at a probed address that doesn't hold the key is not adopted", async () => {
    const def = await load();
    const stranger = new TuyaDevice("192.168.1.77", { version: "3.3", key: OTHER, dps: { "": { "1": true } } });
    const devices = [new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } })];
    const host = network(devices, settingsFor(file(device()), `plug=${IP}@3.3`), { tcpScan: (hosts) => hosts.filter((h) => h === "192.168.1.77") });
    run(def, host, 2);
    devices.splice(0, devices.length, stranger);
    vi.setSystemTime(Date.now() + 31_000);
    run(def, host);
    passTime(def, host, 70);
    expect(host.tcpScans).toHaveLength(1);
    expect(host.logs.some((l) => l.includes("found at a new address"))).toBe(false);
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: false });
  });

  it("a sweep that runs out of its ten seconds gives up before the device that's really there", async () => {
    const def = await load();
    const strangers = Array.from({ length: 11 }, (_, i) => new TuyaDevice(`192.168.1.${60 + i}`, { version: "3.3", key: OTHER }));
    const moved = new TuyaDevice("192.168.1.90", { version: "3.3", dps: { "": { "1": true } } });
    const devices = [new TuyaDevice(IP, { version: "3.3", dps: { "": { "1": true } } })];
    const host = network(devices, settingsFor(file(device()), `plug=${IP}@3.3`), { tcpScan: (hosts) => hosts.filter((h) => [...strangers, moved].some((d) => d.ip === h)) });
    run(def, host, 2);
    devices.splice(0, devices.length, ...strangers, moved);
    vi.setSystemTime(Date.now() + 31_000);
    run(def, host);
    passTime(def, host, 70);
    expect(moved.peers).toHaveLength(0);
    expect(host.logs.some((l) => l.includes("found at a new address"))).toBe(false);
  });

  it("a device on 3.1 can't prove itself, so it's never looked for", async () => {
    const def = await load();
    const host = network([], settingsFor(file(device({ version: "3.1" })), `plug=${IP}@3.1`));
    passTime(def, host, 70);
    expect(host.tcpScans).toHaveLength(0);
  });

  it("a scan that throws is logged, and the link goes on trying", async () => {
    const def = await load();
    const host = network([], settingsFor(file(device()), `plug=${IP}@3.3`), {
      tcpScan: () => {
        throw new Error("network down");
      },
    });
    passTime(def, host, 70);
    expect(host.logs).toContain("Plug: looking for it on the network failed: Error: network down");
    expect(host.logs.filter((l) => l.includes("link down")).length).toBeGreaterThan(1);
  });

  it.each([
    ["gateway.local", 0],
    ["8.8.8.8", 0],
    ["10.0.0.300", 0],
    ["10.1.2.3", 1],
  ])("a link at %s is looked for on its own private network only: %i scans", async (address, scans) => {
    const def = await load();
    const host = network([], settingsFor(file(device({ ip: address })), `plug=${address}@3.3`));
    passTime(def, host, 70);
    expect(host.tcpScans).toHaveLength(scans);
  });

  it("a network is searched once, then not again for five minutes", async () => {
    const def = await load();
    const host = network([], settingsFor(file(device()), `plug=${IP}@3.3`));
    passTime(def, host, 70); // the first failure is the first pass, so a minute of failures takes seventy seconds here
    expect(host.tcpScans).toHaveLength(1);
    passTime(def, host, 200);
    expect(host.tcpScans).toHaveLength(1);
    passTime(def, host, 110);
    expect(host.tcpScans).toHaveLength(2);
  });
});
