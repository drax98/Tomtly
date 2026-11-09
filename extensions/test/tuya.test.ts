import { describe, expect, it, vi } from "vitest";
import { valuesByCode } from "./harness";

// The Tuya extension, against a fake device on each protocol. The fake mirrors internal/extension/tuya_ext_test.go (3.4 and
// 3.5) and tuya_crc_fake_test.go (3.1 to 3.3), but it's driven in-process: the extension writes a frame, the connection's
// write() hands it to the device, and the device's answer is queued for the next read.
import { load, KEY, OTHER, IP, NOW, bytesOf, crcFrame, type TuyaDeviceFault, TuyaDevice, network, device, problems, file, settingsFor, run, PROTOCOLS, EXPECTED_READ, expectedControl, SESSION, tamper, rawFrame35, headerWithLength } from "./tuya-fake";

describe("tuya: every protocol version", () => {
  it.each(PROTOCOLS)("%s: connects, reads the status, takes a push, and sends a control the device decrypts", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true, "4": 150 } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);

    const [card] = def.devices.poll(host.ctx);
    expect(card).toMatchObject({ id: "plug", name: "Plug", online: true, product: "Plug", kind: "plug" });
    expect(valuesByCode(card).switch_1).toMatchObject({ type: "Boolean", writable: true, value: true });
    expect(valuesByCode(card).cur_current).toMatchObject({ type: "Integer", writable: false, value: 150 });
    expect(dev.reads[0]).toEqual(EXPECTED_READ[v]);

    dev.push("", { "1": false });
    run(def, host);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(false);

    vi.setSystemTime(NOW); // the reads above ran the clock on, and the control's time is the fixed one
    def.devices.write(host.ctx, "plug", "switch_1", true);
    const expected = v === "3.4" || v === "3.5" ? 0x0d : 7;
    expect(dev.controls).toEqual([{ cmd: expected, json: expectedControl(v, true) }]);
  });

  it.each(PROTOCOLS)("%s: sends a heartbeat once it's been ten seconds, and not before", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);
    expect(dev.last.commands).not.toContain(9);

    vi.setSystemTime(NOW + 10_001);
    run(def, host);
    expect(dev.last.commands).toContain(9);
  });
});

describe("tuya: frames from the device", () => {
  it.each(PROTOCOLS)("%s: a push that fails its CRC, HMAC or GCM tag is dropped and logged, and the next one is taken", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);

    dev.last.receive(tamper(dev.pushFrame(bytesOf('{"dps":{"1":false}}'))).toString("hex"));
    run(def, host);
    expect(problems(host)).toEqual(["Plug: frame rejected"]);
    expect(def.devices.poll(host.ctx)[0]).toMatchObject({ online: true });
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);

    dev.push("", { "1": false });
    run(def, host);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(false);
  });

  it.each(PROTOCOLS)("%s: a fragment of a frame waits for the rest, and then counts", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);

    const frame = dev.pushFrame(bytesOf('{"dps":{"1":false}}')).toString("hex");
    dev.last.receive(frame.slice(0, 30));
    run(def, host);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);
    expect(problems(host)).toEqual([]);

    dev.last.receive(frame.slice(30));
    run(def, host);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(false);
  });

  it.each(PROTOCOLS)("%s: a header that isn't any protocol's ends the link, and it comes back after ten seconds", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);

    dev.last.receive("ff".repeat(16));
    run(def, host);
    expect(host.logs.at(-1)).toBe("Plug: link down, reconnecting: Error: bad frame prefix, the stream is out of sync");
    expect(dev.peers[0].closed).toBe(true);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);

    vi.setSystemTime(Date.now() + 10_000);
    run(def, host, 2);
    expect(dev.peers).toHaveLength(2);
    expect(def.devices.poll(host.ctx)[0].online).toBe(true);
  });

  it.each(PROTOCOLS)("%s: a length the protocol can't have ends the link, and the log says what it was", async (v) => {
    const small = v === "3.4" ? 10 : v === "3.5" ? 5 : 0;
    for (const length of [70_000, small]) {
      const def = await load();
      const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
      const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
      run(def, host, 2);
      dev.last.receive(headerWithLength(v, length));
      run(def, host);
      expect(host.logs.at(-1)).toBe(`Plug: link down, reconnecting: Error: bad frame length ${length}`);
    }
  });

  it("3.5: a frame that decrypts to less than a return code ends the link", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.5", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.5`));
    run(def, host, 2);
    dev.last.receive(rawFrame35(dev.last.key, 8, Buffer.from("ab", "hex")).toString("hex"));
    run(def, host);
    expect(host.logs.at(-1)).toBe("Plug: link down, reconnecting: Error: frame too short");
  });

  it.each(PROTOCOLS)("%s: a message that isn't JSON is logged, and the link and its values stay as they were", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);
    dev.last.receive(dev.pushFrame(bytesOf("{bad")).toString("hex"));
    run(def, host);
    expect(host.logs.at(-1)).toMatch(/^Plug: bad message: SyntaxError/);
    expect(def.devices.poll(host.ctx)[0].online).toBe(true);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);
  });

  it.each(PROTOCOLS)("%s: a message with no data points, or an empty one, or no JSON at all changes nothing and logs nothing", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);
    for (const text of ['{"x":1}', '{"dps":{}}', "hi"]) dev.last.receive(dev.pushFrame(bytesOf(text)).toString("hex"));
    run(def, host);
    expect(problems(host)).toEqual([]);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);
  });

  it("3.1: a plain-text push is read as JSON, and plain text that isn't JSON is ignored", async () => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: "3.1", dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@3.1`));
    run(def, host, 2);
    dev.last.receive(crcFrame(8, bytesOf("hi")).toString("hex"));
    run(def, host);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);
    dev.last.receive(crcFrame(8, bytesOf('{"dps":{"1":false}}')).toString("hex"));
    run(def, host);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(false);
  });
});

describe("tuya: the session key exchange, 3.4 and 3.5", () => {
  it.each(SESSION)("%s: no answer to the key request is an error, and the link is tried again after ten seconds", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    dev.mute = true;
    run(def, host);
    expect(host.logs).toEqual(["Plug: link down, reconnecting: Error: no answer to the session key request"]);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);

    dev.mute = false;
    vi.setSystemTime(Date.now() + 10_000);
    run(def, host);
    expect(def.devices.poll(host.ctx)[0].online).toBe(true);
    expect(dev.peers).toHaveLength(2);
  });

  const faults: [string, string][] = [
    ["command", "the session key request got command 6"],
    ["short", "the session key reply is too short"],
    ["check", "the session key reply failed its check: is the local key wrong?"],
  ];
  it.each(SESSION.flatMap((v) => faults.map(([fault, message]) => [v, fault, message] as const)))("%s, a handshake whose %s is refused: %s", async (v, fault, message) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, handshake: fault as TuyaDeviceFault, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host);
    expect(host.logs).toEqual([`Plug: link down, reconnecting: Error: ${message}`]);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
  });

  it.each(SESSION.flatMap((v) => ["empty", "split"].map((fault) => [v, fault] as const)))("%s, a handshake that arrives as %s still completes", async (v, fault) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, handshake: fault as TuyaDeviceFault, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host, 2);
    expect(problems(host)).toEqual([]);
    expect(valuesByCode(def.devices.poll(host.ctx)[0]).switch_1.value).toBe(true);
  });

  it.each(SESSION)("%s: a device with another key answers, and is refused without the key ever being logged or shown", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, key: OTHER, replyAnyway: true, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host);
    expect(host.logs).toEqual(["Plug: link down, reconnecting: Error: tuya frame failed authentication"]);
    expect(def.devices.poll(host.ctx)[0].online).toBe(false);
    expect(JSON.stringify([host.logs, def.devices.poll(host.ctx)])).not.toContain(KEY);
  });

  it.each(SESSION)("%s: a device with another key that stays quiet is no answer", async (v) => {
    const def = await load();
    const dev = new TuyaDevice(IP, { version: v, key: OTHER, dps: { "": { "1": true } } });
    const host = network([dev], settingsFor(file(device()), `plug=${IP}@${v}`));
    run(def, host);
    expect(host.logs).toEqual(["Plug: link down, reconnecting: Error: no answer to the session key request"]);
  });
});
