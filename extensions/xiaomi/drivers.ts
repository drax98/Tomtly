// Reading and writing a device, by the way it's driven: a hand table or a spec with MIoT calls, Roborock's or an older family's commands.
import { call, Ctx, Entry, Lost, MIOT_BATCH, NotMiot, POLL_OPS, Refused, Silent, used, Value } from "./miio";
import { Prop, roborockActions, roborockExtras, roborockStatus } from "./models";
import { colourOf, hueAndSaturation, specValue } from "./spec";

const READ_LIMIT_MS = 8000; // one device's turn; a long list goes on from there next poll
const STRIKES_TO_HIDE = 3; // a device still starting up can say this once
// Codes meaning the property isn't there or can't be read, in the forms firmwares use: the short ones from MIoT's error table
// as listed in al-one/hass-xiaomi-miot (core/miot_spec.py), the long ones from XiaoMi/ha_xiaomi_home (miot/i18n/en.json).
// Any other code may pass, so that reading only stays unknown.
const NOT_THERE = [-4001, -4002, -4003, -704030013, -704040002, -704040003, -705201013, -706012013];
const NOT_MIOT = /unknown_method|method not found|-32601/i;

function fromDevice(p: Prop, raw: any): Value {
  if (p.names && typeof raw === "number") return p.names.get(Math.trunc(raw)) ?? String(Math.trunc(raw));
  if (p.type === "Boolean" && typeof raw === "number") return raw !== 0; // Roborock sends 0 and 1
  if (p.divide && typeof raw === "number") return Math.round(raw / p.divide);
  if (p.scale !== 0 && typeof raw === "number") return Math.round(raw * 10 ** p.scale);
  return raw ?? null;
}

function toDevice(p: Prop, value: Value): Value {
  for (const [n, name] of p.names ?? []) {
    if (name === value) return n;
  }
  // Division gives the nearest double to the decimal, which goes out as written: 205 at scale 1 is 20.5, 3 is 0.3.
  if (p.scale && !p.legacy && typeof value === "number") return value / 10 ** p.scale;
  return value;
}

// UDP loses packets, and a read is safe to repeat, so one lost reply doesn't mark a working device offline.
function readOnce(ctx: Ctx, e: Entry, ip: string, params: unknown): any {
  try {
    return call(ctx, e, ip, "get_properties", params);
  } catch (error) {
    if (!(error instanceof Silent) && !(error instanceof Lost)) throw error;
    return call(ctx, e, ip, "get_properties", params);
  }
}

function readMiot(ctx: Ctx, e: Entry, ip: string): Record<string, Value> {
  const props = e.props.filter((p) => p.type !== "Action"); // an Action isn't a property and has nothing to read
  const byDid = new Map(props.map((p): [string, Prop] => [p.did, p]));
  const out: Record<string, Value> = {};
  for (let start = 0; start < props.length; start += MIOT_BATCH) {
    const params = props.slice(start, start + MIOT_BATCH).map((p) => ({ did: p.did, siid: p.siid, piid: p.piid }));
    const reply = readOnce(ctx, e, ip, params);
    if (!Array.isArray(reply)) throw new Refused("get_properties: unexpected reply");
    for (const r of reply) {
      const p = byDid.get(r?.did);
      if (p && r.code === 0) out[p.code] = fromDevice(p, r.value);
    }
  }
  return out;
}

function readRoborock(ctx: Ctx, e: Entry, ip: string): Record<string, Value> {
  const reply = call(ctx, e, ip, "get_status", []);
  if (!Array.isArray(reply) || reply.length === 0) throw new Refused("empty vacuum status");
  const out: Record<string, Value> = {};
  for (const [code, field] of roborockStatus) {
    const p = e.props.find((x) => x.code === code);
    if (p) out[code] = fromDevice(p, reply[0]?.[field]);
  }
  for (const { method, fields } of roborockExtras) {
    try {
      const result = call(ctx, e, ip, method, []);
      const data = Array.isArray(result) && typeof result[0] === "object" && result[0] !== null ? result[0] : result;
      fields.forEach(([code, field], position) => {
        const raw = Array.isArray(data) ? data[typeof field === "number" ? field : position] : data?.[field];
        const p = e.props.find((x) => x.code === code);
        if (p && typeof raw === "number") out[code] = fromDevice(p, raw);
      });
    } catch {
      // Status already answered, so the device is here: these keep their last reading.
    }
  }
  return out;
}

/** Puts one reading into the values, with what's worked out from it. */
function fill(out: Record<string, Value>, p: Prop, raw: unknown): void {
  out[p.code] = specValue(p, raw);
  if (p.colour === "hex" && typeof out[p.code] === "string") {
    const { hue, saturation } = hueAndSaturation(parseInt((out[p.code] as string).slice(1), 16));
    out[`${p.code}_hue`] = hue;
    out[`${p.code}_saturation`] = saturation;
  }
}

/** Stops asking for a property the device keeps saying it doesn't have. It leaves the card with it. */
function strike(e: Entry, did: string): void {
  const count = (e.strikes.get(did) ?? 0) + 1;
  e.strikes.set(did, count);
  if (count >= STRIKES_TO_HIDE) e.hidden.add(did);
}

/** Read from a spec, where any device may differ from what its spec promises. */
function readSpec(ctx: Ctx, e: Entry, ip: string): Record<string, Value> {
  const props = e.props.filter((p) => p.type !== "Action" && !p.writeOnly && p.colour !== "hue" && p.colour !== "saturation" && !e.hidden.has(p.did));
  const byDid = new Map(props.map((p): [string, Prop] => [p.did, p]));
  const out: Record<string, Value> = {};
  const opsAtStart = used();
  const stopAt = Date.now() + READ_LIMIT_MS;
  let start = e.resume < props.length ? e.resume : 0;
  let repeated = false;
  let first = true;
  while (start < props.length) {
    if (!first && (used() - opsAtStart >= POLL_OPS || Date.now() > stopAt)) {
      e.resume = start; // its turn is up
      return out;
    }
    first = false;
    // One property first: a device with the wrong token, or that doesn't speak MIoT, shows it for the cost of one timeout.
    const size = e.answered ? e.batch : 1;
    const batch = props.slice(start, start + size);
    let reply: any;
    try {
      reply = call(ctx, e, ip, "get_properties", batch.map((p) => ({ did: p.did, siid: p.siid, piid: p.piid })));
    } catch (error) {
      if (error instanceof Silent && e.answered && e.batch > 1) {
        if (repeated) e.batch = Math.floor(e.batch / 2); // twice in a row isn't a lost packet: the list is too long for it
        repeated = !repeated;
        continue;
      }
      if (error instanceof Refused) {
        if (NOT_MIOT.test(String(error))) throw new NotMiot(String(error));
        if (size > 1) {
          e.batch = Math.floor(size / 2);
          continue;
        }
        if (e.answered) { // one property breaks the call, and the others still answer
          strike(e, batch[0].did);
          start += 1;
          continue;
        }
      }
      throw error;
    }
    if (!Array.isArray(reply)) {
      // A real Roborock S5 sends a plain result here instead of an error. A device that has answered before is having a bad moment.
      if (!e.answered) throw new NotMiot("get_properties: unexpected reply");
      throw new Refused("get_properties: unexpected reply");
    }
    e.answered = true;
    repeated = false;
    for (const r of reply) {
      const p = byDid.get(r?.did) ?? byDid.get(`${r?.siid}.${r?.piid}`);
      if (!p) continue;
      if (r.code === 0 || (r.code === undefined && r.value !== undefined)) {
        e.strikes.delete(p.did);
        fill(out, p, r.value);
      } else if (NOT_THERE.includes(r.code)) {
        strike(e, p.did);
      }
    }
    start += batch.length;
  }
  e.resume = 0;
  if (props.length === 0 && e.hidden.size > 0) {
    e.note = "It answers, but says it has none of the properties its description lists.";
  }
  return out;
}

/** Read an older device with its own commands. It answers with a list in the order asked, or a call of its own for some values. */
function readLegacy(ctx: Ctx, e: Entry, ip: string): Record<string, Value> {
  const out: Record<string, Value> = {};
  const asked = e.props.filter((p) => !p.legacy?.call);
  for (let start = 0; start < asked.length; start += MIOT_BATCH) {
    const batch = asked.slice(start, start + MIOT_BATCH);
    const reply = call(ctx, e, ip, "get_prop", batch.map((p) => p.legacy?.get));
    if (!Array.isArray(reply)) throw new Refused("get_prop: unexpected reply");
    batch.forEach((p, position) => fill(out, p, reply[position]));
  }
  for (const p of e.props.filter((x) => x.legacy?.call)) {
    try {
      const reply = call(ctx, e, ip, p.legacy?.get ?? "", []);
      fill(out, p, Array.isArray(reply) ? reply[0] : reply);
    } catch (error) {
      if (!(error instanceof Refused)) throw error; // this firmware doesn't have that call, so it stays unknown
    }
  }
  if (e.props.every((p) => out[p.code] === null || out[p.code] === undefined)) throw new Refused("it knows none of this family's properties");
  return out;
}

export function read(ctx: Ctx, e: Entry, ip: string): Record<string, Value> {
  if (e.driver === "spec") return readSpec(ctx, e, ip);
  if (e.driver === "legacy") return readLegacy(ctx, e, ip);
  return e.roborock ? readRoborock(ctx, e, ip) : readMiot(ctx, e, ip);
}

export function writeProp(ctx: Ctx, e: Entry, p: Prop, value: Value): void {
  if (e.roborock) {
    if (p.type === "Action") {
      for (const method of roborockActions[String(value)] ?? []) call(ctx, e, e.ip, method, []);
      return;
    }
    call(ctx, e, e.ip, p.code === "sound_volume" ? "change_sound_volume" : "set_custom_mode", [toDevice(p, value)]);
    return;
  }
  if (p.legacy) {
    const { set, setOn, setOff } = p.legacy;
    const method = value === true && setOn ? setOn : value === false && setOff ? setOff : set;
    if (!method) throw new Error(`${p.code} has no command`);
    const first = typeof value === "boolean" ? (value ? "on" : "off") : value;
    const reply = call(ctx, e, e.ip, method, setOn ? [] : [first, ...(p.legacy.with ?? [])]);
    if ((Array.isArray(reply) ? reply[0] : reply) !== "ok") throw new Refused(`${p.code}: device refused: ${JSON.stringify(reply)}`);
    return;
  }
  if (p.type === "Action") {
    const ids = p.actions?.get(String(value));
    if (!ids) throw new Error(`${p.code} has no action ${value}`);
    const reply = call(ctx, e, e.ip, "action", { did: `call-${ids[0]}-${ids[1]}`, siid: ids[0], aiid: ids[1], in: [] });
    if (reply?.code !== 0 && reply?.code !== 1) throw new Refused(`${value}: device refused: ${JSON.stringify(reply)}`);
    return;
  }
  let sending = toDevice(p, value);
  if (p.colour === "hue" || p.colour === "saturation") {
    const whole = p.code.replace(/_(hue|saturation)$/, "");
    const hue = p.colour === "hue" ? Number(value) : Number(e.values[`${whole}_hue`] ?? 0);
    const saturation = p.colour === "saturation" ? Number(value) : Number(e.values[`${whole}_saturation`] ?? 100);
    sending = colourOf(hue, saturation);
  }
  const reply = call(ctx, e, e.ip, "set_properties", [{ did: p.did, siid: p.siid, piid: p.piid, value: sending }]);
  // Some firmware answers 1: accepted, and still working on it.
  if (!Array.isArray(reply) || reply.length === 0 || (reply[0]?.code !== 0 && reply[0]?.code !== 1)) throw new Refused(`${p.code}: device refused: ${JSON.stringify(reply)}`);
  // So a saturation written straight after a hue builds on the new hue, not the one last read.
  if (p.colour === "hue" || p.colour === "saturation") e.values[p.code] = Number(value);
}

/** UDP can lose a packet. Only that is worth repeating; the host's own refusals and the device's aren't. */
export function isLost(error: unknown): boolean {
  return error instanceof Lost || /timeout|connection refused/i.test(String(error));
}

