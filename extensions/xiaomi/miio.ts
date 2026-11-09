// The miIO wire protocol, and the count of what it costs.
import { Prop } from "./models";

export const PORT = 54321;
export const TIMEOUT_MS = 1500;
export const MIOT_BATCH = 15; // several models cut off longer get_properties calls
export const HELLO = "21310020" + "ff".repeat(28);
// The service stops an extension that goes over 600 operations a minute, and each miIO call is two. This is what polling may
// use, leaving the rest for commands, so a big home is polled less often rather than stopped.
export const OPS_PER_MINUTE = 400;
export const POLL_OPS = Math.ceil(OPS_PER_MINUTE / 6); // one poll's share at one poll every 10 seconds

export type Ctx = ExtensionContext<any>;
export type Value = boolean | number | string | null;

/** The device answered and refused, so repeating it won't help. */
export class Refused extends Error {}
/** A reply that isn't authentic or isn't ours counts as lost, as a timeout does. */
export class Lost extends Error {}
/** The device said hello, then gave no answer at all to the request. A list too long for it looks like this. */
export class Silent extends Lost {}
/** The device answers, but not to MIoT calls. */
export class NotMiot extends Error {}

export interface Entry {
  id: string;
  name: string;
  model: string;
  kind: string;
  props: Prop[];
  roborock: boolean;
  /** Which way it's read. "none" is never polled, and note says why. A "spec" device with no props is waiting for its spec. */
  driver: "table" | "roborock" | "spec" | "legacy" | "none";
  urn: string;
  note: string; // why it has no values, shown on its card
  batch: number; // how many properties it takes in one call
  answered: boolean;
  resume: number; // where the last read stopped, for a list too long for one turn
  hidden: Set<string>; // properties it said it doesn't have
  strikes: Map<string, number>;
  ip: string;
  key: { token: string; aes: string; iv: string };
  nextId: number;
  values: Record<string, Value>; // by prop code; stays while the device is offline, so scripts keep the last reading
  online: boolean;
  everTried: boolean;
  nextTry: number;
  failingSince: number;
  lastSweep: number;
}

const spent: number[] = []; // when each recent operation happened

export function spend(n = 1): void {
  for (let i = 0; i < n; i++) spent.push(Date.now());
}

/** Operations in the last minute. Sliding, because the service's minute starts at a moment we don't know. */
export function used(): number {
  const since = Date.now() - 61_000;
  while (spent.length > 0 && spent[0] < since) spent.shift();
  return spent.length;
}

function u32(n: number): string {
  return (n >>> 0).toString(16).padStart(8, "0");
}

export function deriveKey(ctx: Ctx, token: string): Entry["key"] {
  const aes = ctx.crypto.hash("md5", token);
  return { token, aes, iv: ctx.crypto.hash("md5", aes + token) };
}

/** One request and its first reply. */
function ask(ctx: Ctx, ip: string, data: string): string {
  spend();
  return ctx.udp.request(ip, PORT, data, { timeoutMs: TIMEOUT_MS });
}

export function parseHello(reply: string): { id: number; stamp: number } {
  if (reply.length < 64 || !reply.startsWith("2131")) throw new Error("the device sent a bad hello reply");
  return { id: parseInt(reply.slice(16, 24), 16), stamp: parseInt(reply.slice(24, 32), 16) };
}

function packet(ctx: Ctx, key: Entry["key"], id: number, stamp: number, payload: string): string {
  const size = (32 + payload.length / 2).toString(16).padStart(4, "0");
  const head = "2131" + size + "00000000" + u32(id) + u32(stamp);
  return head + ctx.crypto.hash("md5", head + key.token + payload) + payload;
}

/** Returns the reply's JSON, or null if it isn't authentic: too short, wrong checksum, or it doesn't decrypt. */
function parseReply(ctx: Ctx, key: Entry["key"], reply: string): any {
  if (reply.length <= 64 || !reply.startsWith("2131")) return null;
  const payload = reply.slice(64);
  if (ctx.crypto.hash("md5", reply.slice(0, 32) + key.token + payload) !== reply.slice(32, 64)) return null;
  try {
    const plain = ctx.crypto.decrypt({ mode: "cbc", key: key.aes, iv: key.iv, data: payload, padding: true });
    return JSON.parse(ctx.crypto.text(plain).replace(/\0+$/, "")); // some firmwares end the JSON with a NUL byte
  } catch {
    return null;
  }
}

/** Sends one command and returns its result. Throws Refused if the device refuses, and Lost for a reply that isn't its answer. */
export function call(ctx: Ctx, e: Entry, ip: string, method: string, params: unknown): any {
  const { id: deviceId, stamp } = parseHello(ask(ctx, ip, HELLO));
  const id = ++e.nextId;
  const body = ctx.crypto.hex(JSON.stringify({ id, method, params }));
  const payload = ctx.crypto.encrypt({ mode: "cbc", key: e.key.aes, iv: e.key.iv, data: body, padding: true });
  let answer: string;
  try {
    answer = ask(ctx, ip, packet(ctx, e.key, deviceId, (stamp + 1) >>> 0, payload));
  } catch (error) {
    throw /timeout/i.test(String(error)) ? new Silent(`${method}: ${error}`) : error;
  }
  const reply = parseReply(ctx, e.key, answer);
  if (reply === null || reply.id !== id) throw new Lost(`${method}: no authentic answer`);
  if (reply.error !== undefined && reply.error !== null) throw new Refused(`${method}: ${JSON.stringify(reply.error)}`);
  return reply.result;
}

