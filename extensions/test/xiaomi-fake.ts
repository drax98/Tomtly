// Shared by the xiaomi tests: the fake devices and the helpers that drive them.
import { afterEach, it, vi } from "vitest";
import { crypto, loadExtension, makeHost, TIMEOUT, type Host, type Reply } from "./harness";

export const load = () => loadExtension(() => import("../xiaomi/extension.ts"));

export const TOKEN = "00112233445566778899aabbccddeeff";

export const AES = crypto.hash("md5", TOKEN); // what miIO derives from the token, as the extension does

export const IV = crypto.hash("md5", AES + TOKEN);

export const HELLO = "21310020" + "ff".repeat(28);

export const IP = "192.168.1.60";

export const SPEC_BASE = "https://miot-spec.org/miot-spec-v2/instance?type=";

export const BULB_URN = "urn:miot-spec-v2:device:light:0000A001:acme-bulb:1";

export const u32 = (n: number) => (n >>> 0).toString(16).padStart(8, "0");

export type Answer = { result?: unknown; error?: unknown } | undefined;

/**
 * One miIO device. It answers the hello the way a real one does, and sealed requests with its token. Its answers come from
 * `answer`, and a method can be made to drop, come back corrupted, or with another request's id, once or while it's set.
 */
export class Miio {
  calls: { id: number; method: string; params: any; stamp: number }[] = [];
  offline = false; // no reply to anything, not even the hello
  silent = false; // hello answered, nothing else
  badHello = false;
  nulSuffix = false;
  corrupt = false;
  wrongId = false;
  dropOnce = new Set<string>();
  corruptOnce = new Set<string>();

  constructor(public ip: string, public did: number, public answer: (method: string, params: any) => Answer) {}

  reply(data: string): string | null {
    if (this.offline) return null;
    if (data === HELLO) return this.badHello ? "zz" : "2131" + "0020" + "00000000" + u32(this.did) + u32(1000) + "00".repeat(16);
    if (this.silent) return null;
    if (data.length <= 64 || data.slice(0, 4) !== "2131") return null;
    const payload = data.slice(64);
    if (crypto.hash("md5", data.slice(0, 32) + TOKEN + payload) !== data.slice(32, 64)) return null;
    const req = JSON.parse(crypto.text(crypto.decrypt({ mode: "cbc", key: AES, iv: IV, data: payload, padding: true })));
    const stamp = parseInt(data.slice(24, 32), 16);
    this.calls.push({ id: req.id, method: req.method, params: req.params, stamp });
    if (this.dropOnce.has(req.method)) {
      this.dropOnce.delete(req.method);
      return null;
    }
    const answer = this.answer(req.method, req.params);
    if (answer === undefined) return null;
    const body: any = { id: this.wrongId ? req.id + 1000 : req.id, ...answer };
    const corrupt = this.corrupt || this.corruptOnce.delete(req.method);
    return this.pack(body, stamp, corrupt);
  }

  pack(body: object, stamp: number, corrupt: boolean): string {
    let plain = crypto.hex(JSON.stringify(body));
    if (this.nulSuffix) plain += "00";
    const payload = crypto.encrypt({ mode: "cbc", key: AES, iv: IV, data: plain, padding: true });
    const head = "2131" + (32 + payload.length / 2).toString(16).padStart(4, "0") + "00000000" + u32(this.did) + u32(stamp);
    const sum = crypto.hash("md5", head + TOKEN + payload);
    return head + (corrupt ? "00" + sum.slice(2) : sum) + payload;
  }

  methods(name: string) {
    return this.calls.filter((c) => c.method === name);
  }
}

/** Answers get_properties from a map of did to value, the way a device reports what it has. */
export function properties(values: Record<string, unknown>): (method: string, params: any) => Answer {
  return (method, params) => {
    if (method !== "get_properties") return undefined;
    return { result: params.map((p: any) => ({ did: p.did, siid: p.siid, piid: p.piid, code: 0, value: values[p.did] ?? 0 })) };
  };
}

/** A network with these devices on it. Each answers at its own address and nowhere else. */
export function network(devices: Miio[], extra: { settings?: Record<string, unknown>; removed?: string[]; fetch?: (url: string, options: FetchOptions) => Reply } = {}): Host {
  const find = (ip: string) => devices.find((d) => d.ip === ip);
  return makeHost({
    settings: extra.settings ?? {},
    removed: extra.removed,
    udpRequest(ip, _port, data) {
      const reply = find(ip)?.reply(data) ?? null;
      if (reply === null) throw new Error(TIMEOUT);
      return reply;
    },
    udpScan(hosts, _port, data) {
      return devices.flatMap((d) => {
        if (!hosts.includes(d.ip)) return [];
        const reply = d.reply(data);
        return reply === null ? [] : [{ from: d.ip, data: reply }];
      });
    },
    fetch: extra.fetch,
  });
}

/** The devices file the cloud extractor writes: a list of accounts, each with homes, each with devices. */
export function devicesFile(...devices: Record<string, unknown>[]): string {
  return JSON.stringify([{ homes: [{ devices }] }]);
}

export function device(over: Record<string, unknown> = {}): Record<string, unknown> {
  return { did: "1234567", name: "Desk lamp", model: "yeelink.light.ceiling5", localip: IP, token: TOKEN, ...over };
}

export const LAMP_VALUES = { "2.1": true, "2.2": 60, "2.4": 4000, "2.3": 2, "2.5": false };

afterEach(() => {
  vi.useRealTimers();
});
