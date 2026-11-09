// The Tuya LAN protocol, per TinyTuya (core/XenonDevice.py, core/message_helper.py, core/header.py):
//   3.1  55AA, CRC32. Plain JSON both ways, except controls: "3.1" + md5 part + base64(AES-ECB). Pushes come in that form too.
//   3.2  as 3.3, always the device22 way.
//   3.3  55AA, CRC32. AES-ECB with the local key, and the version header outside the encryption, on commands 7 and 13 only.
//   3.4  55AA, HMAC-SHA256. Session key. AES-ECB with the version header inside the encryption.
//   3.5  6699, AES-GCM. Session key.
// device22 is TinyTuya's name for firmware that answers a normal status read with "data unvalid" and wants command 13 listing
// the data points as nulls. 3.2 always does. TinyTuya no longer goes by the id being 22 characters, and neither does this:
// many ordinary devices have such ids, so the reply decides.
const CMD_SESS_START = 3;
const CMD_SESS_RESP = 4;
const CMD_SESS_FINISH = 5;
const CMD_CONTROL_OLD = 7;
export const CMD_HEARTBEAT = 9;
const CMD_DP_QUERY = 0x0a;
const CMD_CONTROL = 0x0d;
const CMD_QUERY = 0x10;
const PREFIX_55AA = "000055aa";
const SUFFIX_55AA = "0000aa55";
const PREFIX_6699 = "00006699";
const SUFFIX_6699 = "00009966";
const MAX_FRAME = 65536; // a larger claimed size means a desynced stream
export const BAD_AUTH = "tuya frame failed authentication";

type Crypto = { crypto: ExtensionCrypto };

export interface Session {
  conn: TcpConnection;
  version: string;
  realKey: string;
  key: string; // equals realKey until the session key is negotiated
  seq: number;
  buffer: string;
  proven: boolean; // the peer holds the key: a finished handshake, or a message that decrypted to JSON
  unvalid: boolean; // it answered "data unvalid"
  started: number;
}

export interface Frame {
  cmd: number;
  payload: string; // decrypted for 3.5, still encrypted for the rest
}

export interface Command {
  cmd: number;
  body: object;
}

export function hasSessionKey(version: string): boolean {
  return version === "3.4" || version === "3.5";
}

export function newSession(ctx: Crypto, conn: TcpConnection, version: string, localKey: string): Session {
  const realKey = ctx.crypto.hex(localKey);
  return { conn, version, realKey, key: realKey, seq: 1, buffer: "", proven: false, unvalid: false, started: Date.now() };
}

function u32(n: number): string {
  return n.toString(16).padStart(8, "0");
}

function readU32(hex: string, offset: number): number {
  return parseInt(hex.slice(offset * 2, (offset + 4) * 2), 16);
}

function xor(a: string, b: string): string {
  let out = "";
  for (let i = 0; i < a.length; i += 2) {
    out += (parseInt(a.slice(i, i + 2), 16) ^ parseInt(b.slice(i, i + 2), 16)).toString(16).padStart(2, "0");
  }
  return out;
}

const CRC_TABLE: number[] = [];
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let bit = 0; bit < 8; bit++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  CRC_TABLE.push(c >>> 0);
}

/** IEEE CRC-32, as binascii.crc32 and Go's crc32.ChecksumIEEE. */
export function crc32(hex: string): number {
  let c = 0xffffffff;
  for (let i = 0; i < hex.length; i += 2) c = CRC_TABLE[(c ^ parseInt(hex.slice(i, i + 2), 16)) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function encodeCrc(ctx: Crypto, s: Session, cmd: number, payload: string): string {
  let body = payload;
  if (s.version !== "3.1") {
    body = ctx.crypto.encrypt({ mode: "ecb", key: s.key, data: payload, padding: true });
    if (cmd === CMD_CONTROL_OLD || cmd === CMD_CONTROL) body = ctx.crypto.hex(s.version) + "00".repeat(12) + body;
  } else if (cmd === CMD_CONTROL_OLD) {
    const sealed = ctx.crypto.toBase64(ctx.crypto.encrypt({ mode: "ecb", key: s.key, data: payload, padding: true }));
    const signed = ctx.crypto.hash("md5", ctx.crypto.hex(`data=${sealed}||lpv=3.1||`) + s.realKey);
    body = ctx.crypto.hex(`3.1${signed.slice(8, 24)}${sealed}`);
  }
  const framed = PREFIX_55AA + u32(s.seq++) + u32(cmd) + u32(body.length / 2 + 8) + body;
  return framed + u32(crc32(framed)) + SUFFIX_55AA;
}

/** Encrypts and frames an outgoing command with the current key. */
function encode(ctx: Crypto, s: Session, cmd: number, payload: string): string {
  if (!hasSessionKey(s.version)) return encodeCrc(ctx, s, cmd, payload);
  if (cmd === CMD_CONTROL) payload = ctx.crypto.hex(s.version) + "00".repeat(12) + payload; // only control messages carry the version header
  const seq = s.seq++;
  if (s.version === "3.5") {
    const iv = ctx.crypto.random(12);
    const head = "0000" + u32(seq) + u32(cmd) + u32(12 + payload.length / 2 + 16);
    return PREFIX_6699 + head + iv + ctx.crypto.encrypt({ mode: "gcm", key: s.key, iv, aad: head, data: payload }) + SUFFIX_6699;
  }
  const body = ctx.crypto.encrypt({ mode: "ecb", key: s.key, data: payload, padding: true });
  const framed = PREFIX_55AA + u32(seq) + u32(cmd) + u32(body.length / 2 + 32 + 4) + body;
  return framed + ctx.crypto.hmac("sha256", s.key, framed) + SUFFIX_55AA;
}

/** The next whole frame in the buffer, or null if more bytes are needed. Throws BAD_AUTH for one that fails authentication. */
export function nextFrame(ctx: Crypto, s: Session): Frame | null {
  if (s.buffer.length < 32) return null;
  const prefix = s.buffer.slice(0, 8);
  if (prefix === PREFIX_55AA && !hasSessionKey(s.version)) {
    const cmd = readU32(s.buffer, 8);
    const length = readU32(s.buffer, 12);
    if (length > MAX_FRAME || length < 8) throw new Error(`bad frame length ${length}`);
    if (s.buffer.length < (16 + length) * 2) return null;
    const end = (16 + length - 8) * 2;
    const signed = s.buffer.slice(0, end);
    const crc = parseInt(s.buffer.slice(end, end + 8), 16);
    s.buffer = s.buffer.slice((16 + length) * 2);
    // A CRC only catches damage. What proves the sender is that the payload decrypts, in decode.
    if (crc32(signed) !== crc) throw new Error(BAD_AUTH);
    const payload = signed.slice(32);
    // Replies start with a return code and pushes don't, as in tuyapi's parser: a code's top three bytes are zero.
    return { cmd, payload: payload.startsWith("000000") ? payload.slice(8) : payload };
  }
  if (prefix === PREFIX_55AA) {
    const cmd = readU32(s.buffer, 8);
    const length = readU32(s.buffer, 12);
    if (length > MAX_FRAME || length < 4 + 32 + 4) throw new Error(`bad frame length ${length}`);
    if (s.buffer.length < (16 + length) * 2) return null;
    const signed = s.buffer.slice(0, (16 + length - 36) * 2);
    const mac = s.buffer.slice((16 + length - 36) * 2, (16 + length - 4) * 2);
    const payload = s.buffer.slice(20 * 2, (16 + length - 36) * 2); // the first 4 bytes are the return code
    s.buffer = s.buffer.slice((16 + length) * 2);
    if (ctx.crypto.hmac("sha256", s.key, signed) !== mac) throw new Error(BAD_AUTH);
    return { cmd, payload };
  }
  if (prefix === PREFIX_6699) {
    if (s.buffer.length < 36) return null;
    const cmd = readU32(s.buffer, 10);
    const length = readU32(s.buffer, 14);
    if (length > MAX_FRAME || length < 12 + 16) throw new Error(`bad frame length ${length}`);
    if (s.buffer.length < (18 + length + 4) * 2) return null;
    const aad = s.buffer.slice(4 * 2, 18 * 2);
    const iv = s.buffer.slice(18 * 2, 30 * 2);
    const sealed = s.buffer.slice(30 * 2, (18 + length) * 2);
    s.buffer = s.buffer.slice((18 + length + 4) * 2);
    let plain: string;
    try {
      plain = ctx.crypto.decrypt({ mode: "gcm", key: s.key, iv, aad, data: sealed });
    } catch {
      throw new Error(BAD_AUTH);
    }
    if (plain.length < 8) throw new Error("frame too short");
    return { cmd, payload: plain.slice(8) }; // the first 4 bytes are the return code
  }
  throw new Error("bad frame prefix, the stream is out of sync");
}

/** Runs the three-step session key exchange. Sends nothing but its own frames, so it can prove who a device is. */
export function negotiate(ctx: Crypto, s: Session, limitMs: number): void {
  const nonce = ctx.crypto.random(16);
  s.conn.write(encode(ctx, s, CMD_SESS_START, nonce));
  const deadline = Date.now() + limitMs;
  let reply: Frame | null = null;
  while (reply === null || reply.payload === "") {
    reply = nextFrame(ctx, s);
    if (reply === null || reply.payload === "") {
      const left = deadline - Date.now();
      if (left <= 0) throw new Error("no answer to the session key request");
      s.buffer += s.conn.read({ timeoutMs: Math.min(left, 1000) });
    }
  }
  if (reply.cmd !== CMD_SESS_RESP) throw new Error(`the session key request got command ${reply.cmd}`);
  const payload = s.version === "3.4" ? ctx.crypto.decrypt({ mode: "ecb", key: s.realKey, data: reply.payload }) : reply.payload;
  if (payload.length < 96) throw new Error("the session key reply is too short");
  const remote = payload.slice(0, 32);
  if (ctx.crypto.hmac("sha256", s.realKey, nonce) !== payload.slice(32, 96)) {
    throw new Error("the session key reply failed its check: is the local key wrong?");
  }
  s.conn.write(encode(ctx, s, CMD_SESS_FINISH, ctx.crypto.hmac("sha256", s.realKey, remote)));
  const mixed = xor(nonce, remote);
  s.key = s.version === "3.4"
    ? ctx.crypto.encrypt({ mode: "ecb", key: s.realKey, data: mixed })
    : ctx.crypto.encrypt({ mode: "gcm", key: s.realKey, iv: nonce.slice(0, 24), data: mixed }).slice(0, 32);
  s.proven = true;
}

/**
 * 3.1 to 3.3 have no handshake, so this is the proof instead: one status read, which changes nothing, and a reply that
 * decrypts with the local key. ECB has no nonce, so someone who recorded a reply could replay it, but they can't read
 * what's written to them afterwards. 3.1 replies in plain text, which proves nothing, so 3.1 can't use this.
 */
export function proveByRead(ctx: Crypto, s: Session, read: Command, limitMs: number): void {
  sendCommand(ctx, s, read);
  const deadline = Date.now() + limitMs;
  for (;;) {
    for (let frame = nextFrame(ctx, s); frame !== null; frame = nextFrame(ctx, s)) {
      decode(ctx, s, frame);
      if (s.proven) return;
    }
    const left = deadline - Date.now();
    if (left <= 0) throw new Error("no readable answer");
    s.buffer += s.conn.read({ timeoutMs: Math.min(left, 1000) });
  }
}

function unpad(hex: string): string {
  if (hex === "") return hex;
  const n = parseInt(hex.slice(-2), 16);
  return n >= 1 && n <= 16 && n * 2 <= hex.length ? hex.slice(0, -n * 2) : hex;
}

/** A decrypted message: JSON and the proof the sender has the key, or "data unvalid", which only a key holder encrypts. */
function readPlain(ctx: Crypto, s: Session, plain: string): any {
  const text = ctx.crypto.text(plain);
  if (text.includes("data unvalid")) {
    s.unvalid = true;
    s.proven = true;
    return null;
  }
  if (!text.startsWith("{")) return null;
  const msg = JSON.parse(text);
  s.proven = true;
  return msg;
}

function decodeCrc(ctx: Crypto, s: Session, payload: string): any {
  if (payload.startsWith(ctx.crypto.hex("3.1"))) { // whatever the version, TinyTuya reads these this way
    const sealed = ctx.crypto.fromBase64(ctx.crypto.text(payload.slice(19 * 2)));
    return readPlain(ctx, s, unpad(ctx.crypto.decrypt({ mode: "ecb", key: s.key, data: sealed })));
  }
  if (s.version === "3.1") {
    if (!payload.startsWith("7b")) return null;
    const msg = JSON.parse(ctx.crypto.text(payload));
    s.proven = true; // shortcut: plain text proves nothing on 3.1, there's no way to tell without sending a command
    return msg;
  }
  if (ctx.crypto.text(payload).includes("data unvalid")) { // in the clear, so no proof
    s.unvalid = true;
    return null;
  }
  // The header is outside the encryption, and a device22 header can carry another version, so a stray length gives it away.
  const body = payload.startsWith(ctx.crypto.hex(s.version)) || payload.length % 32 !== 0 ? payload.slice(30) : payload;
  if (body === "") return null;
  return readPlain(ctx, s, unpad(ctx.crypto.decrypt({ mode: "ecb", key: s.key, data: body })));
}

/** The JSON a device sent in a frame, or null for an empty or non-JSON one. */
export function decode(ctx: Crypto, s: Session, f: Frame): any {
  if (f.payload === "") return null;
  if (!hasSessionKey(s.version)) return decodeCrc(ctx, s, f.payload);
  let payload = f.payload;
  if (s.version === "3.4") payload = unpad(ctx.crypto.decrypt({ mode: "ecb", key: s.key, data: payload }));
  const header = ctx.crypto.hex(s.version);
  if (payload.startsWith(header) && payload.length >= 30) payload = payload.slice(30);
  if (payload === "") return null;
  if (!payload.startsWith("7b")) {
    if (s.version === "3.4" && ctx.crypto.text(payload).includes("data unvalid")) s.unvalid = true;
    return null;
  }
  return JSON.parse(ctx.crypto.text(payload));
}

/** Older firmware puts data points and the sub-device id at the top level, 3.4 and later under "data". */
export function dpsOf(msg: any): { dps: Record<string, unknown> | undefined; cid: string } {
  const data = msg.data !== null && typeof msg.data === "object" ? msg.data : {};
  const dps = msg.dps !== null && typeof msg.dps === "object" ? msg.dps : data.dps !== null && typeof data.dps === "object" ? data.dps : undefined;
  const cid = typeof msg.cid === "string" && msg.cid !== "" ? msg.cid : typeof data.cid === "string" ? data.cid : "";
  return { dps, cid };
}

export function sendCommand(ctx: Crypto, s: Session, c: Command): void {
  s.conn.write(encode(ctx, s, c.cmd, ctx.crypto.hex(JSON.stringify(c.body))));
}

/** A status read, as payload_dict in TinyTuya's XenonDevice.py builds it. requested is the device22 list of data points. */
export function statusRead(version: string, device22: boolean, id: string, cid: string | undefined, requested: string[]): Command {
  const t = Math.floor(Date.now() / 1000);
  const withCid = cid === undefined ? {} : { cid };
  if (device22) {
    const dps: Record<string, null> = {};
    for (const dp of requested.length > 0 ? requested : ["1"]) dps[dp] = null;
    return { cmd: CMD_CONTROL, body: { devId: id, uid: id, t: String(t), dps, ...withCid } };
  }
  if (hasSessionKey(version)) return { cmd: CMD_QUERY, body: withCid };
  return { cmd: CMD_DP_QUERY, body: cid === undefined ? { gwId: id, devId: id, uid: id, t: String(t) } : { t, cid } };
}

/** A command that sets values. */
export function control(version: string, id: string, cid: string | undefined, dps: Record<string, unknown>): Command {
  const t = Math.floor(Date.now() / 1000);
  if (hasSessionKey(version)) {
    const data: Record<string, unknown> = { dps };
    const msg: Record<string, unknown> = { protocol: 5, t, data };
    if (cid !== undefined) {
      msg.cid = cid;
      data.cid = cid;
      data.ctype = 0;
    }
    return { cmd: CMD_CONTROL, body: msg };
  }
  return { cmd: CMD_CONTROL_OLD, body: cid === undefined ? { devId: id, uid: id, t: String(t), dps } : { t, cid, dps } };
}
