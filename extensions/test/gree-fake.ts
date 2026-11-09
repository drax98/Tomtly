// Shared by the gree tests: the fake devices and the helpers that drive them.
import { afterEach, it, vi } from "vitest";
import { crypto, loadExtension, makeHost, TIMEOUT, type Host } from "./harness";

// The published keys and GCM parameters every Gree unit uses, as in internal/extension/gree_ext_test.go.
export const ECB_KEY = "a3K8Bx%2r8Y7#xDh";

export const GCM_KEY = "{yxAHAY_Lm6pbC/<";

export const GCM_AAD = "qualcomm-test";

export const GCM_NONCE = "5440784449675a516c5e6313";

export const UNIT_KEY = "UnitKeyForTests1"; // the key a unit hands out when bound

export const MAC = "aabbccddeeff";

export const ADDRESS = "192.168.1.44";

export const load = () => loadExtension(() => import("../gree/extension.ts"));

/** Seals a body the way the unit does: ECB with padding, or GCM with the tag sent apart from the data. */
export function seal(key: string, gcm: boolean, body: object): { pack: string; tag?: string } {
  const data = crypto.hex(JSON.stringify(body));
  if (!gcm) return { pack: crypto.toBase64(crypto.encrypt({ mode: "ecb", key: crypto.hex(key), data, padding: true })) };
  const out = crypto.encrypt({ mode: "gcm", key: crypto.hex(key), iv: GCM_NONCE, aad: crypto.hex(GCM_AAD), data });
  return { pack: crypto.toBase64(out.slice(0, -32)), tag: crypto.toBase64(out.slice(-32)) };
}

/** Opens what the extension sent. Throws if the key is wrong. */
export function open(key: string, gcm: boolean, env: { pack: string; tag?: string }): any {
  const data = crypto.fromBase64(env.pack);
  const text = gcm
    ? crypto.decrypt({ mode: "gcm", key: crypto.hex(key), iv: GCM_NONCE, aad: crypto.hex(GCM_AAD), data: data + crypto.fromBase64(env.tag ?? "") })
    : crypto.decrypt({ mode: "ecb", key: crypto.hex(key), data, padding: true });
  return JSON.parse(crypto.text(text));
}

export interface UnitOptions {
  gcm?: boolean;
  /** Raw protocol values by column, as the unit reports them. */
  state?: Record<string, number>;
  /** Columns the unit has no answer for. */
  lacks?: string[];
  rejectCommands?: boolean;
  /** Answers a bind with a reply that doesn't decrypt. */
  garbageBind?: boolean;
  /** Answers a bind with an envelope that has no data. */
  noPackBind?: boolean;
  /** Answers a bind with a refusal. */
  refuseBind?: boolean;
  /** Answers a bind with an empty key. */
  emptyKeyBind?: boolean;
  /** Answers a scan without a MAC. */
  scanWithoutMac?: boolean;
  /** Answers a status with this body instead. */
  statusReply?: object;
  /** Replies to a scan with a GCM tag, though it binds over ECB, as some firmware does. */
  scanTag?: boolean;
}

export const DEFAULT_STATE = { Pow: 1, Mod: 4, SetTem: 24, WdSpd: 3, Quiet: 0, Tur: 0, Lig: 1, TemSen: 63, SwUpDn: 1, SwingLfRig: 6, Blo: 1 };

/** A Gree air conditioner on the network. Answers only the scheme it speaks, and forgets nothing it was told. */
export class Unit {
  silent = false;
  state: Record<string, number>;
  /** Every request it understood, decrypted, with the key it arrived under. */
  requests: { body: any; env: any; key: string }[] = [];
  binds = 0;

  constructor(public address: string, public mac = MAC, public o: UnitOptions = {}) {
    this.state = { ...DEFAULT_STATE, ...o.state };
  }

  /** Answers one datagram, as hex, or returns null for silence. */
  answer(data: string): string | null {
    if (this.silent) return null;
    const gcm = !!this.o.gcm;
    const scanKey = gcm ? GCM_KEY : ECB_KEY;
    const req = JSON.parse(crypto.text(data));
    if (req.t === "scan") {
      const body = this.o.scanWithoutMac ? { t: "dev" } : { t: "dev", mac: this.mac, cid: this.mac };
      const tagged = gcm || !!this.o.scanTag;
      return crypto.hex(JSON.stringify({ t: "pack", ...seal(tagged ? GCM_KEY : ECB_KEY, tagged, body) }));
    }
    if ((req.tag !== undefined) !== gcm) return null; // the other scheme gets silence
    let key: string | null = null;
    let body: any = null;
    for (const k of [UNIT_KEY, scanKey]) {
      try {
        body = open(k, gcm, req);
        key = k;
        break;
      } catch {
        // not this key
      }
    }
    if (body === null || key === null) return null;
    this.requests.push({ body, env: req, key });
    return this.respond(body, key, gcm);
  }

  private respond(body: any, key: string, gcm: boolean): string | null {
    const wrap = (plain: object) => crypto.hex(JSON.stringify({ t: "pack", i: 0, uid: 0, cid: "", tcid: "", ...seal(key, gcm, plain) }));
    if (body.t === "bind") {
      this.binds++;
      if (this.o.noPackBind) return crypto.hex(JSON.stringify({ t: "pack" }));
      if (this.o.garbageBind) return crypto.hex(JSON.stringify({ t: "pack", pack: Buffer.alloc(16, 7).toString("base64"), tag: Buffer.alloc(16, 9).toString("base64") }));
      if (this.o.refuseBind) return wrap({ t: "bindfail" });
      return wrap({ t: "bindok", mac: this.mac, key: this.o.emptyKeyBind ? "" : UNIT_KEY });
    }
    if (body.t === "status") {
      if (this.o.statusReply) return wrap(this.o.statusReply);
      const cols = (body.cols as string[]).filter((c) => c in this.state && !(this.o.lacks ?? []).includes(c));
      return wrap({ t: "dat", mac: this.mac, cols, dat: cols.map((c) => this.state[c]) });
    }
    if (body.t === "cmd") {
      if (this.o.rejectCommands) return wrap({ t: "res", r: 400 });
      (body.opt as string[]).forEach((col, i) => (this.state[col] = body.p[i]));
      return wrap({ t: "res", r: 200 });
    }
    return null;
  }

  requestsOf(t: string): { body: any; env: any; key: string }[] {
    return this.requests.filter((r) => r.body.t === t);
  }
}

/** A host with the given units on the network, reachable at their addresses. */
export function network(units: Unit[], settings: Record<string, unknown> = {}, extra: { removed?: string[] } = {}): Host {
  return makeHost({
    settings: { host: ADDRESS, name: "Air conditioner", ...settings },
    udpRequest(host, _port, data) {
      const unit = units.find((u) => u.address === host);
      const reply = unit?.answer(data) ?? null;
      if (reply === null) throw new Error(TIMEOUT);
      return reply;
    },
    udpScan(hosts, _port, data) {
      return units.flatMap((u) => {
        if (!hosts.includes(u.address)) return [];
        const reply = u.answer(data);
        return reply === null ? [] : [{ from: u.address, data: reply }];
      });
    },
    removed: extra.removed,
  });
}

afterEach(() => {
  vi.useRealTimers();
});

export const ALL_COLS = ["Pow", "Mod", "SetTem", "WdSpd", "Quiet", "Tur", "Lig", "Buzzer_ON_OFF", "TemSen", "SwUpDn", "SwingLfRig", "Air", "Blo", "Health", "SwhSlp", "StHt", "SvSt"];
