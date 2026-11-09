// Shared by the tuya tests: the fake devices and the helpers that drive them.
import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes } from "node:crypto";
import { afterEach, beforeEach, it, vi } from "vitest";
import { FakeConnection, loadExtension, makeHost, type Host, type HostOptions } from "./harness";

// The Tuya extension, against a fake device on each protocol. The fake mirrors internal/extension/tuya_ext_test.go (3.4 and
// 3.5) and tuya_crc_fake_test.go (3.1 to 3.3), but it's driven in-process: the extension writes a frame, the connection's
// write() hands it to the device, and the device's answer is queued for the next read.

export const load = () => loadExtension(() => import("../tuya/extension.ts"));

export const KEY = "0123456789abcdef"; // the local key in the devices file

export const OTHER = "fedcba9876543210"; // a key nothing on the file holds

export const IP = "192.168.1.50";

export const NOW = 1_700_000_000_000; // so every t the extension sends is 1700000000

export const SEC = 1_700_000_000;

export const bytesOf = (text: string) => Buffer.from(text, "utf8");

export const hasSession = (version: string) => version === "3.4" || version === "3.5";

export const headerOf = (version: string) => Buffer.concat([bytesOf(version), Buffer.alloc(12)]); // "3.x" plus 12 zero bytes, 15 in all

export const u32 = (n: number) => {
  const b = Buffer.alloc(4);
  b.writeUInt32BE(n >>> 0);
  return b;
};

export function pad(data: Buffer): Buffer {
  const n = 16 - (data.length % 16);
  return Buffer.concat([data, Buffer.alloc(n, n)]);
}

export function unpad(data: Buffer): Buffer {
  return data.subarray(0, data.length - data[data.length - 1]);
}

export function ecb(key: Buffer, data: Buffer, encrypt: boolean): Buffer {
  const c = encrypt ? createCipheriv("aes-128-ecb", key, null) : createDecipheriv("aes-128-ecb", key, null);
  c.setAutoPadding(false);
  return Buffer.concat([c.update(data), c.final()]);
}

export function gcmSeal(key: Buffer, iv: Buffer, aad: Buffer, plain: Buffer): Buffer {
  const c = createCipheriv("aes-128-gcm", key, iv, { authTagLength: 16 });
  c.setAAD(aad);
  return Buffer.concat([c.update(plain), c.final(), c.getAuthTag()]);
}

export function gcmOpen(key: Buffer, iv: Buffer, aad: Buffer, sealed: Buffer): Buffer | null {
  if (sealed.length < 16) return null;
  const d = createDecipheriv("aes-128-gcm", key, iv, { authTagLength: 16 });
  d.setAAD(aad);
  d.setAuthTag(sealed.subarray(sealed.length - 16));
  try {
    return Buffer.concat([d.update(sealed.subarray(0, sealed.length - 16)), d.final()]);
  } catch {
    return null;
  }
}

export function hmac(key: Buffer, data: Buffer): Buffer {
  return createHmac("sha256", key).update(data).digest();
}

/** The 3.1 part that says the sealed body came from someone who holds the key. */
export function md5Part(sealed: string, key: string): string {
  return createHash("md5").update(bytesOf(`data=${sealed}||lpv=3.1||${key}`)).digest("hex").slice(8, 24);
}

export const CRC_TABLE = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let bit = 0; bit < 8; bit++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});

/** IEEE CRC-32, written out here so the test doesn't reuse the extension's table. */
export function crc32(data: Buffer): number {
  let c = 0xffffffff;
  for (const b of data) c = CRC_TABLE[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

/** A 55AA frame with a CRC, as 3.1 to 3.3 send them. The length counts the payload, the CRC and the suffix. */
export function crcFrame(cmd: number, payload: Buffer): Buffer {
  const signed = Buffer.concat([Buffer.from("000055aa", "hex"), u32(1), u32(cmd), u32(payload.length + 8), payload]);
  return Buffer.concat([signed, u32(crc32(signed)), Buffer.from("0000aa55", "hex")]);
}

/**
 * A 3.4 (55AA with an HMAC) or 3.5 (6699 with AES-GCM) frame. The payload gets its four return-code bytes first, as the
 * fakes do. An empty payload stays empty: the frame carries the return code only.
 */
export function sessionFrame(version: string, key: Buffer, cmd: number, payload: Buffer): Buffer {
  if (version === "3.5") {
    const head = Buffer.concat([Buffer.from("00006699", "hex"), Buffer.from("0000", "hex"), u32(1), u32(cmd), u32(12 + 4 + payload.length + 16)]);
    const iv = randomBytes(12);
    const sealed = gcmSeal(key, iv, head.subarray(4), Buffer.concat([u32(0), payload]));
    return Buffer.concat([head, iv, sealed, Buffer.from("00009966", "hex")]);
  }
  const body = Buffer.concat([u32(0), payload.length ? ecb(key, pad(payload), true) : Buffer.alloc(0)]);
  const framed = Buffer.concat([Buffer.from("000055aa", "hex"), u32(1), u32(cmd), u32(body.length + 36), body]);
  return Buffer.concat([framed, hmac(key, framed), Buffer.from("0000aa55", "hex")]);
}

/** The 3.1 sealed form: base64 of AES-ECB, with the md5 part in front. */
export function seal31(json: Buffer, key: string): Buffer {
  const sealed = ecb(bytesOf(key), pad(json), true).toString("base64");
  return bytesOf("3.1" + md5Part(sealed, key) + sealed);
}

/** The key a 3.4 or 3.5 session continues under, from the two nonces. */
export function sessionKey(version: string, real: Buffer, nonce: Buffer, remote: Buffer): Buffer {
  const x = Buffer.from(nonce.map((b, i) => b ^ remote[i]));
  return version === "3.4" ? ecb(real, x, true) : gcmSeal(real, nonce.subarray(0, 12), Buffer.alloc(0), x).subarray(0, 16);
}

export function send(p: Peer, frame: Buffer): void {
  p.receive(frame.toString("hex"));
}

export interface Opened {
  cmd: number;
  plain: Buffer;
}

/** A frame a session version sent, opened with the connection's current key, or null if it doesn't authenticate. */
export function openSession(p: Peer, version: string, buf: Buffer): Opened | null {
  const control = (cmd: number, plain: Buffer) => {
    // Control messages carry the version header inside the encryption, on 3.4 and 3.5 alike.
    const headed = (cmd === 0x0d || cmd === 7) && plain.subarray(0, 15).equals(headerOf(version));
    return { cmd, plain: headed ? plain.subarray(15) : plain };
  };
  if (buf.readUInt32BE(0) === 0x6699) {
    const len = buf.readUInt32BE(14);
    if (buf.length !== 18 + len + 4) return null;
    const plain = gcmOpen(p.key, buf.subarray(18, 30), buf.subarray(4, 18), buf.subarray(30, 18 + len));
    return plain === null ? null : control(buf.readUInt32BE(10), plain);
  }
  const len = buf.readUInt32BE(12);
  if (buf.length !== 16 + len) return null;
  const at = 16 + len - 36;
  if (!hmac(p.key, buf.subarray(0, at)).equals(buf.subarray(at, at + 32))) return null;
  return control(buf.readUInt32BE(8), unpad(ecb(p.key, buf.subarray(16, at), false)));
}

/** A 55AA frame of 3.1 to 3.3, opened with the device's key, or null if its CRC or its body doesn't check out. */
export function openCrc(d: TuyaDevice, buf: Buffer): Opened | null {
  if (buf.readUInt32BE(0) !== 0x55aa) return null;
  const cmd = buf.readUInt32BE(8);
  const len = buf.readUInt32BE(12);
  if (buf.length !== 16 + len) return null;
  const at = 16 + len - 8;
  if (crc32(buf.subarray(0, at)) !== buf.readUInt32BE(at)) return null;
  let body = buf.subarray(16, at);
  if (d.o.version === "3.1") {
    if (cmd !== 7) return { cmd, plain: body };
    const text = body.toString("utf8");
    const sealed = text.slice(19);
    if (text.slice(0, 3) !== "3.1" || text.slice(3, 19) !== md5Part(sealed, d.keyText)) return null;
    return { cmd, plain: unpad(ecb(d.key, Buffer.from(sealed, "base64"), false)) };
  }
  if (cmd === 7 || cmd === 0x0d) {
    if (!body.subarray(0, 15).equals(headerOf(d.o.version))) return null;
    body = body.subarray(15);
  }
  if (body.length % 16 !== 0) return null;
  const plain = ecb(d.key, body, false);
  const n = plain[plain.length - 1]; // a wrong key rarely leaves valid padding, and the Go fake refuses such a request too
  if (n < 1 || n > 16 || n > plain.length) return null;
  return { cmd, plain: plain.subarray(0, plain.length - n) };
}

export function parseJson(plain: Buffer): any {
  try {
    return JSON.parse(plain.toString("utf8"));
  } catch {
    return null;
  }
}

/** One TCP connection the extension opened to a device. Frames it writes reach the device at once, and its answers queue. */
export class Peer extends FakeConnection {
  key: Buffer; // the local key until a session key is negotiated
  remote = Buffer.alloc(0); // the device's half of the session key exchange
  nonce = Buffer.alloc(0); // the extension's half
  commands: number[] = []; // the command of every frame received, in order

  constructor(host: string, port: number, options: TcpOptions | undefined, private dev: TuyaDevice) {
    super(host, port, options);
    this.key = dev.key; // the device's own key, until a session key replaces it
  }

  write(data: string): void {
    super.write(data);
    this.dev.receive(this, Buffer.from(data, "hex"));
  }

  read(options?: SocketOptions): string {
    // A quiet line costs time, so a handshake waiting for an answer runs out its limit, as it would for real.
    const chunk = super.read(options);
    if (chunk === "") vi.setSystemTime(Date.now() + 1000);
    return chunk;
  }
}

export interface DeviceOptions {
  version: string;
  key?: string; // the device's own key, if it's not KEY
  dps?: Record<string, Record<string, unknown>>; // by cid, "" for the device itself
  device22?: boolean; // answers a status read with "data unvalid"
  plainText?: boolean; // answers in clear, as something without the key would
  replyAnyway?: boolean; // answers a request it can't read, with its own key, as a stranger would
  handshake?: "check" | "short" | "command" | "empty" | "split"; // a fault in the 3.4 and 3.5 key exchange
}

export type TuyaDeviceFault = NonNullable<DeviceOptions["handshake"]>;

/** A device on one protocol. Records what it was sent, and answers from its dps and its options. */
export class TuyaDevice {
  mute = false; // answers nothing at all, not even the handshake
  peers: Peer[] = [];
  controls: { cmd: number; json: any }[] = []; // every control it decrypted
  reads: any[] = []; // every status read it decrypted
  readonly key: Buffer;
  readonly keyText: string;

  constructor(public ip: string, public o: DeviceOptions) {
    this.keyText = o.key ?? KEY;
    this.key = bytesOf(this.keyText);
  }

  connect(host: string, port: number, options?: TcpOptions): Peer {
    const p = new Peer(host, port, options, this);
    this.peers.push(p);
    return p;
  }

  get last(): Peer {
    return this.peers[this.peers.length - 1];
  }

  /** The frame a push of this JSON is, on the protocol this device speaks, on the connection's current key. */
  pushFrame(json: Buffer, p: Peer = this.last): Buffer {
    const v = this.o.version;
    if (hasSession(v)) return sessionFrame(v, p.key, 8, json);
    return crcFrame(8, v === "3.1" ? seal31(json, this.keyText) : Buffer.concat([headerOf(v), ecb(this.key, pad(json), true)]));
  }

  /** Something the device sends on its own, such as a battery sensor reporting. */
  push(cid: string, dps: Record<string, unknown>): void {
    send(this.last, this.pushFrame(bytesOf(JSON.stringify(cid ? { data: { dps, cid } } : { dps }))));
  }

  receive(p: Peer, buf: Buffer): void {
    if (this.mute) return;
    const session = hasSession(this.o.version);
    const cmd = buf.readUInt32BE(buf.readUInt32BE(0) === 0x6699 ? 10 : 8);
    p.commands.push(cmd);
    const opened = session ? openSession(p, this.o.version, buf) : openCrc(this, buf);
    if (opened === null) {
      if (this.o.replyAnyway) {
        if (session && cmd === 3) this.handshake(p, Buffer.alloc(16));
        else if (!session) this.answer(p, cmd, bytesOf('{"dps":{"1":true}}'));
      }
      return;
    }
    this.serve(p, opened.cmd, opened.plain);
  }

  /** The device's half of the session key exchange. A fault here is one of the ways a real device gets it wrong. */
  private handshake(p: Peer, nonce: Buffer): void {
    const v = this.o.version;
    const remote = randomBytes(16);
    p.remote = remote;
    p.nonce = nonce;
    const mac = this.o.handshake === "check" ? Buffer.alloc(32, 1) : hmac(this.key, nonce);
    const payload = this.o.handshake === "short" ? remote : Buffer.concat([remote, mac]);
    const cmd = this.o.handshake === "command" ? 6 : 4;
    if (this.o.handshake === "empty") send(p, sessionFrame(v, this.key, 4, Buffer.alloc(0)));
    const frame = sessionFrame(v, this.key, cmd, payload).toString("hex");
    if (this.o.handshake === "split") {
      p.receive(frame.slice(0, 40), frame.slice(40));
      return;
    }
    p.receive(frame);
  }

  /** A reply to a request, in the form the version uses. */
  private answer(p: Peer, cmd: number, plain: Buffer | null): void {
    const v = this.o.version;
    const payload = plain ?? Buffer.alloc(0);
    if (hasSession(v)) return send(p, sessionFrame(v, p.key, cmd, payload));
    let body = Buffer.alloc(0);
    if (payload.length) body = v === "3.1" || this.o.plainText ? payload : ecb(this.key, pad(payload), true);
    send(p, crcFrame(cmd, Buffer.concat([u32(0), body])));
  }

  private serve(p: Peer, cmd: number, plain: Buffer): void {
    const v = this.o.version;
    if (hasSession(v) && cmd === 3) return this.handshake(p, plain.subarray(0, 16));
    if (hasSession(v) && cmd === 5) {
      if (plain.equals(hmac(this.key, p.remote))) p.key = sessionKey(v, this.key, p.nonce, p.remote);
      return;
    }
    if (cmd === 9) return this.answer(p, 9, null);
    const msg = parseJson(plain);
    if (hasSession(v)) {
      if (cmd === 0x0d) return void this.controls.push({ cmd, json: msg });
      if (cmd === 0x10) return this.query(p, msg);
      return;
    }
    if (cmd === 7) return void this.controls.push({ cmd, json: msg });
    if (cmd === 0x0a || cmd === 0x0d) return this.read(p, cmd, msg);
  }

  private read(p: Peer, cmd: number, msg: any): void {
    this.reads.push(msg);
    if (cmd === 0x0a && this.o.device22) return this.answer(p, cmd, bytesOf("json obj data unvalid"));
    const cid = typeof msg?.cid === "string" ? msg.cid : "";
    const dps = this.o.dps?.[cid];
    if (dps === undefined) return this.answer(p, cmd, null);
    this.answer(p, cmd, bytesOf(JSON.stringify(cid ? { dps, cid } : { dps, devId: msg?.devId })));
  }

  /** A 3.4 or 3.5 status query: the device's dps as a push, root or child. */
  private query(p: Peer, msg: any): void {
    this.reads.push(msg);
    const cid = typeof msg?.cid === "string" ? msg.cid : "";
    const dps = this.o.dps?.[cid];
    if (dps === undefined) return;
    this.answer(p, 8, bytesOf(JSON.stringify(cid ? { data: { dps, cid } } : { dps })));
  }
}

/** A network with these devices on it. Each answers at its own address and nowhere else. */
export function network(devices: TuyaDevice[], settings: Record<string, unknown>, extra: { removed?: string[]; tcpScan?: HostOptions["tcpScan"] } = {}): Host {
  return makeHost({
    settings,
    removed: extra.removed,
    tcpScan: extra.tcpScan,
    tcpConnect(host, port, options) {
      const d = devices.find((x) => x.ip === host);
      if (d === undefined) throw new Error(`connection to ${host} refused`);
      return d.connect(host, port, options);
    },
  });
}

/** One entry of the devices file, as python -m tinytuya wizard writes it. */
export function device(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: "plug", name: "Plug", key: KEY, category: "cz", product_name: "Plug", ip: IP, version: "3.3",
    mapping: {
      "1": { code: "switch_1", type: "Boolean", values: {} },
      "4": { code: "cur_current", type: "Integer", values: { min: 0, max: 30000 } },
    },
    ...over,
  };
}

/** The log lines that say something went wrong, without the one each connection writes when it comes up. */
export const problems = (host: Host): string[] => host.logs.filter((l) => !l.includes("connected at"));

export const file = (...devices: Record<string, unknown>[]): string => JSON.stringify(devices);

export const settingsFor = (devices: string, hosts: string) => ({ devices, hosts });

/** Runs background.run once per tick: the first run connects, the next ones read what the device answered. */
export function run(def: any, host: Host, times = 1): void {
  for (let i = 0; i < times; i++) def.background.run(host.ctx);
}

export const PROTOCOLS = ["3.1", "3.2", "3.3", "3.4", "3.5"];

// What the extension asks a device for, decrypted by the device, on each protocol. Times are the fixed NOW.
export const EXPECTED_READ: Record<string, unknown> = {
  "3.1": { gwId: "plug", devId: "plug", uid: "plug", t: "1700000000" },
  "3.2": { devId: "plug", uid: "plug", t: "1700000000", dps: { "1": null, "4": null } }, // device22: the data points as nulls
  "3.3": { gwId: "plug", devId: "plug", uid: "plug", t: "1700000000" },
  "3.4": {},
  "3.5": {},
};

// What a write of switch_1 = value decrypts to. Old protocols name the device; 3.4 and 3.5 wrap the dps in data.
export const expectedControl = (v: string, value: boolean): unknown =>
  hasSession(v) ? { protocol: 5, t: SEC, data: { dps: { "1": value } } } : { devId: "plug", uid: "plug", t: "1700000000", dps: { "1": value } };

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
});

export const SESSION = ["3.4", "3.5"];

/** The frame with one byte of its check value flipped. The CRC, HMAC or GCM tag sits just before the four suffix bytes. */
export function tamper(frame: Buffer): Buffer {
  const out = Buffer.from(frame);
  out[out.length - 5] ^= 0xff;
  return out;
}

/** A 3.5 frame whose plain body is exactly these bytes, for the frames a device would never make on its own. */
export function rawFrame35(key: Buffer, cmd: number, plain: Buffer): Buffer {
  const head = Buffer.concat([Buffer.from("00006699", "hex"), Buffer.from("0000", "hex"), u32(1), u32(cmd), u32(12 + plain.length + 16)]);
  const iv = randomBytes(12);
  return Buffer.concat([head, iv, gcmSeal(key, iv, head.subarray(4), plain), Buffer.from("00009966", "hex")]);
}

/** The first bytes of a frame of this version, with a length field that says `length`. */
export function headerWithLength(version: string, length: number): string {
  const prefix = version === "3.5" ? "00006699" + "0000" + "00000001" + "00000009" : "000055aa" + "00000001" + "00000009";
  return prefix + u32(length).toString("hex");
}

/** The mapping a thermostat's wizard entry has: the values the device reports, and the ones it takes. */
export const THERMOSTAT_MAPPING = {
  "1": { code: "switch", type: "Boolean", values: {} },
  "2": { code: "temp_set", type: "Integer", values: { min: 50, max: 300, scale: 1, step: 5, unit: "℃" } },
  "3": { code: "temp_current", type: "Integer", values: { min: 0, max: 500, scale: 0, step: 1, unit: "℃" } },
  "4": { code: "mode", type: "Enum", values: { range: ["auto", "manual"] } },
  "5": { code: "factory_reset", type: "Boolean", values: {} },
  "6": { code: "add_ele", type: "Integer", values: { unit: "度", min: 0, max: 100 } },
  "7": { code: "schedule", type: "Json", values: "" },
  "8": { code: "fault", type: "Bitmap", values: { label: ["a"] } },
  "10": { code: "c_f", type: "Enum", values: { range: ["c", "f"] } },
};

/** A gateway, and a thermostat and a door sensor behind it. The door sensor sleeps, so it may never report. */
export function gatewayFile(gateway: Record<string, unknown> = {}): Record<string, unknown>[] {
  return [
    device({ id: "gw1", name: "Gateway", category: "wg2", product_name: "GW", mapping: {}, version: "3.4", ...gateway }),
    device({ id: "th1", name: "Thermostat", category: "wk", product_name: "TRV", node_id: "cid-th", mapping: THERMOSTAT_MAPPING }),
    device({ id: "door1", name: "Door", category: "mcs", product_name: "Door", node_id: "cid-door", mapping: { "101": { code: "prm_switch", type: "Boolean", values: {} } } }),
  ];
}

export const byId = (cards: ExtensionDevice[], id: string): ExtensionDevice => {
  const card = cards.find((c) => c.id === id);
  if (card === undefined) throw new Error(`no card ${id}`);
  return card;
};

/** Moves the clock on in ten second steps, and runs the background once at each step. */
export function passTime(def: any, host: Host, seconds: number): void {
  for (let s = 0; s < seconds; s += 10) {
    vi.setSystemTime(Date.now() + 10_000);
    run(def, host);
  }
}

/** A light, with the mapping and reported values it's given. */
export function lamp(mapping: Record<string, unknown>, over: Record<string, unknown> = {}): Record<string, unknown> {
  return device({ id: "lamp", name: "Lamp", category: "dj", product_name: "Bulb", mapping, ...over });
}

export const COLOUR_MAPPING = {
  "2": { code: "work_mode", type: "Enum", values: { range: ["white", "colour"] } },
  "3": { code: "colour_data", type: "Json", values: {} },
};
