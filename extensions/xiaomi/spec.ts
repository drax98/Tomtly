// Turns a model's MIoT spec into device values, so a model nobody wrote a table for still works. The rules are general:
// nothing below names a model. Spec format: https://miot-spec.org/miot-spec-v2/instance?type=<urn>
import { deviceType, kindOf, reportOnly } from "./kinds";
import { Prop } from "./models";

const MAX_VALUES = 64; // shortcut: a spec's values past this aren't shown, 7 of 524 sampled specs reach it, raise it if one matters
const MAX_ACTIONS = 24;
const MAX_CHOICES = 64;
const MAX_NAME = 40;
export const MAX_TEXT = 200;

export interface SpecProperty {
  iid: number;
  name: string;
  format: string;
  read: boolean;
  write: boolean;
  unit?: string;
  range?: number[];
  list?: [number, string][];
}

export interface SpecService {
  iid: number;
  name: string;
  props: SpecProperty[];
  actions: [number, string][];
}

// Never shown, by service, property or action, in its name or description. Could brick or unpair a device, or say who owns it.
const UNSAFE_ANYWHERE = /firmware|factory|wifi|wi-fi|debug|restart|rollback|initiali[sz]|calibrat|cipher|privacy|flash|learn|country|password|passwd|token|secret|rfid|reboot|serial|upgrade|unbind|pairing|ssid|handshake|account|device-information|device information|message-router|重置|复位|恢复出厂|固件|升级|配网|解绑|绑定|调试/i;
// Short words, whole only: "total" holds ota, "machine" mac, "snore" sn. Reset and restore only have to start a word, so that
// resetclean goes and preset stays. Clear, delete and format lose data.
const UNSAFE_WORD = /(^|[-_. ])(ota|mac|sn|uid|uuid|did|ip|bind|binding|pair|ble|zigbee|network|server|cloud|region|dns|proxy|connect|disconnect|trim|add-remote|remote-add|remote-del|lbs|gps|location|latitude|longitude|wipe|erase|delete|clear|remove|(sd|storage|disk)-format|format-(sd|storage|disk))([-_. \d]|$)|(^|[-_. ])(reset|restore)/i;
// Shown but read-only: a person decides when a part was replaced, and writing a life level is how some devices reset it.
const CONSUMABLE = /filter|brush|mop-life|dust-bag|desiccant|deodorant|sponge|consumable/;
// Shown but read-only, and no actions: these open or disarm something. "physical-controls-locked" is a child lock and stays.
const OPENS_SOMETHING = /(^|-)(unlock|open-door|disarm|arming|lock-action|lock-status)(-|$)/;

const INTEGERS = ["uint8", "uint16", "uint32", "int8", "int16", "int32", "int64"];
const UNITS: Record<string, string> = {
  percentage: "%", celsius: "°C", fahrenheit: "°F", kelvin: "K", seconds: "s", minutes: "min", hours: "h", days: "days", lux: "lx", pascal: "Pa", watt: "W",
  metre: "m", meter: "m", gram: "g", kilogram: "kg", arcdegrees: "°", "μg/m3": "µg/m³", "mg/m3": "mg/m³", "m3/h": "m³/h", none: "", rgb: "",
};

export function unsafe(...texts: unknown[]): boolean {
  return texts.some((t) => typeof t === "string" && (UNSAFE_ANYWHERE.test(t) || UNSAFE_WORD.test(t)));
}

/** The <name> in urn:miot-spec-v2:property:<name>:... */
function nameOf(urn: unknown): string {
  const name = typeof urn === "string" ? (urn.split(":")[3] ?? "").toLowerCase() : "";
  return /^[a-z0-9][a-z0-9._-]{0,60}$/.test(name) ? name : "";
}

const isIid = (n: unknown): n is number => Number.isInteger(n) && (n as number) > 0 && (n as number) < 65536;

function compactProperty(p: any): SpecProperty | null {
  const name = nameOf(p?.type);
  const access: unknown[] = Array.isArray(p?.access) ? p.access : [];
  const read = access.includes("read");
  const write = access.includes("write");
  const format = String(p?.format);
  // No read or write is an action's argument or an event's. Formats beyond these aren't anything the page could show.
  if (!isIid(p?.iid) || !name || (!read && !write) || unsafe(name, p.description)) return null;
  if (format !== "bool" && format !== "float" && format !== "string" && !INTEGERS.includes(format)) return null;
  const out: SpecProperty = { iid: p.iid, name, format, read, write };
  if (typeof p.unit === "string" && p.unit !== "none" && p.unit.length <= 16) out.unit = p.unit;
  const range = p["value-range"];
  if (Array.isArray(range) && range.length >= 2 && range.every((n) => Number.isFinite(n))) out.range = range.slice(0, 3);
  if (Array.isArray(p["value-list"]) && INTEGERS.includes(format)) {
    const list = p["value-list"].filter((v: any) => Number.isInteger(v?.value)).slice(0, MAX_CHOICES);
    if (list.length > 0) out.list = list.map((v: any): [number, string] => [v.value, typeof v.description === "string" ? v.description.trim().toLowerCase().slice(0, MAX_NAME) : ""]);
  }
  return out;
}

/**
 * Keeps only what a device's values are built from, without anything unsafe, so what's cached stays small: the largest of 524 sampled specs, 51 KB,
 * is 17 KB here, and most are under 3 KB. Returns null for something that isn't a spec. Anything can be in it, since it comes from another server.
 */
export function compactSpec(doc: any): SpecService[] | null {
  if (!Array.isArray(doc?.services)) return null;
  const out: SpecService[] = [];
  for (const s of doc.services) {
    const name = nameOf(s?.type);
    if (!isIid(s?.iid) || !name || unsafe(name, s.description)) continue;
    const props = (Array.isArray(s.properties) ? s.properties : []).map(compactProperty).filter((p: SpecProperty | null) => p !== null) as SpecProperty[];
    const actions: [number, string][] = [];
    for (const a of Array.isArray(s.actions) ? s.actions : []) {
      const action = nameOf(a?.type);
      // The host carries a choice from a list and nothing more, so an action that wants arguments can't be sent.
      if (isIid(a?.iid) && action && (!Array.isArray(a.in) || a.in.length === 0) && !unsafe(action, a.description)) actions.push([a.iid, action]);
    }
    if (props.length > 0 || actions.length > 0) out.push({ iid: s.iid, name, props, actions });
  }
  return out;
}

const word = (name: string) => name.replace(/[^a-z0-9]+/g, "_");

/** How many decimals a float's step has, if it's few enough to keep as a whole number and write back exactly. */
function decimalsOf(step: number): number | null {
  for (let s = 0; s <= 3; s++) {
    const scaled = step * 10 ** s;
    if (scaled >= 1 && Math.abs(scaled - Math.round(scaled)) < 1e-9) return s;
  }
  return null;
}

function usableRange(p: SpecProperty): { min: number; max: number; step: number } | null {
  if (!p.range || !(p.range[0] < p.range[1])) return null;
  return { min: p.range[0], max: p.range[1], step: p.range[2] > 0 ? p.range[2] : 1 };
}

/** Choice names a person can tell apart. An empty one is its number, a repeated one gets its number added. */
function choices(p: SpecProperty, rename: (name: string) => string): [number, string][] {
  const seen = new Set<string>();
  return (p.list ?? []).map(([value, description]): [number, string] => {
    let name = rename(description) || String(value);
    if (seen.has(name)) name = `${name} (${value})`;
    seen.add(name);
    return [value, name];
  });
}

function valueProps(s: SpecService, p: SpecProperty, code: string, locked: boolean): Prop[] {
  const base = { code, siid: s.iid, piid: p.iid, scale: 0, did: `${s.iid}.${p.iid}` };
  const writable = p.write && !locked && !CONSUMABLE.test(`${s.name} ${p.name}`) && !OPENS_SOMETHING.test(p.name) && s.name !== "lock";
  const extra: Partial<Prop> = {};
  if (!p.read) extra.writeOnly = true;
  const unit = p.unit === undefined ? p.unit : UNITS[p.unit] ?? p.unit;
  if (unit) extra.unit = unit;
  if (!p.read && !writable) return [];
  if (p.format === "bool") return [{ ...base, ...extra, type: "Boolean", writable }];
  if (p.format === "string") return p.read ? [{ ...base, ...extra, type: "String", writable: false }] : []; // the host writes no text
  if (p.list) {
    const named = choices(p, (name) => (code === "control" && name === "pause" ? "stop" : name)); // the cover card's words
    // A device's mode can hide a reset as one of its choices: it can still report it, but not be asked for it.
    const range = named.filter(([, name]) => !unsafe(name)).map(([, name]) => name);
    return [{ ...base, ...extra, type: "Enum", writable: writable && range.length > 0, names: new Map(named), range }];
  }
  const range = usableRange(p);
  if (p.format === "float") {
    const decimals = range && writable ? decimalsOf(range.step) : null;
    if (range && decimals !== null) {
      const raw = (n: number) => Math.round(n * 10 ** decimals);
      return [{ ...base, ...extra, type: "Integer", writable, scale: decimals, min: raw(range.min), max: raw(range.max), step: raw(range.step) }];
    }
    // shortcut: a writable float with no range or a step finer than 0.001 is read-only, the host can't carry it exactly.
    return p.read ? [{ ...base, ...extra, type: "Number", writable: false, digits: 4 }] : [];
  }
  if (p.format === "uint32" && (p.unit === "rgb" || p.name === "color") && (range === null || range.max <= 0xffffff)) {
    const hex: Prop = { ...base, ...extra, type: "String", writable: false, colour: "hex" };
    if (!writable) return p.read ? [hex] : [];
    const part = (name: "hue" | "saturation", max: number): Prop => ({ ...base, code: `${code}_${name}`, type: "Integer", writable: true, colour: name, min: 0, max, step: 1 });
    return [hex, part("hue", 360), part("saturation", 100)];
  }
  if (range && writable) return [{ ...base, ...extra, type: "Integer", writable, min: range.min, max: range.max, step: range.step }];
  // shortcut: an integer with no range is read-only, since nothing says what it may hold; use its format's limits if one is missed.
  return p.read ? [{ ...base, ...extra, type: "Integer", writable: false }] : [];
}

const ANYWHERE: Record<string, string> = {
  temperature: "temperature", "relative-humidity": "humidity", "battery-level": "battery", "physical-controls-locked": "child_lock", "pm2.5-density": "pm25", "filter-life-level": "filter_life",
};

/** The codes the page and shared scripts already understand, see docs/devices.md. The hand tables use these too. */
function alias(kind: string, main: boolean, name: string): string | undefined {
  if (name === "on") return main ? "power" : undefined;
  if (kind === "vacuum" && main) return { status: "state", mode: "fan_speed", fault: "error_code" }[name];
  if (kind === "cover") return { "motor-control": "control", "target-position": "percent_control", "current-position": "percent_state" }[name];
  if (kind === "climate" && (name === "temperature" || name === "target-temperature")) return name === "temperature" ? "temp_current" : "temp_set";
  return ANYWHERE[name];
}

const VACUUM_ACTIONS: Record<string, string> = { "start-sweep": "start", "stop-sweeping": "stop", "start-charge": "dock", identify: "find" };

/** The value's name: stable for a spec version, and unique, since the host refuses a device with a repeat. */
function namer() {
  const used = new Set(["command", "unavailable"]);
  return (wanted: (string | undefined)[], s: SpecService, iid: number, name: string): string => {
    const candidates = wanted.filter((c): c is string => c !== undefined);
    const code = candidates.find((c) => !used.has(c)) ?? `${word(s.name)}_${s.iid}_${iid}_${word(name)}`;
    used.add(code);
    return code;
  };
}

/** Everything a spec offers, as a table like the ones in models.ts. Main service first, so it's what a long list keeps. */
export function specProps(urn: string, services: SpecService[]): Prop[] {
  const type = deviceType(urn);
  const kind = kindOf(type);
  const locked = reportOnly(type);
  const hasOn = (s: SpecService) => s.props.some((p) => p.name === "on" && p.write);
  const main = services.find((s) => s.name === type) ?? services.find((s) => hasOn(s) && !/indicator|ambient|night|screen|alarm/.test(s.name)) ?? services[0];
  const ordered = services.filter((s) => s === main).concat(services.filter((s) => s !== main));
  // Several switch services are the channels of a multi-gang switch: the card shows switch_1 and so on, not one "power".
  const gangs = services.filter((s) => s.name === "switch" && hasOn(s));
  const named = namer();
  const out: Prop[] = [];
  for (const s of ordered) {
    for (const p of s.props) {
      const gang = gangs.length > 1 && s.name === "switch" && p.name === "on" ? `switch_${gangs.indexOf(s) + 1}` : undefined;
      const own = word(p.name.indexOf(s.name) === 0 || s === main ? p.name : `${s.name}_${p.name}`);
      const code = named([gang, gang ? undefined : alias(kind, s === main, p.name), own], s, p.iid, p.name);
      out.push(...valueProps(s, p, code, locked));
    }
  }
  const values = out.slice(0, MAX_VALUES);
  const actions = new Map<string, [number, number]>();
  if (!locked) {
    for (const s of ordered) {
      if (s.name === "lock") continue;
      for (const [aiid, name] of s.actions) {
        if (OPENS_SOMETHING.test(name) || CONSUMABLE.test(`${s.name} ${name}`) || actions.size >= MAX_ACTIONS) continue;
        const short = kind === "vacuum" && s === main ? VACUUM_ACTIONS[name] ?? word(name) : word(name);
        const choice = !actions.has(short) ? short : `${word(s.name)}_${s.iid}_${aiid}_${word(name)}`;
        actions.set(choice, [s.iid, aiid]);
      }
    }
  }
  if (actions.size > 0) {
    values.push({ code: "command", siid: 0, piid: 0, did: "0.0", scale: 0, type: "Action", writable: true, range: Array.from(actions.keys()), actions });
  }
  return values;
}

/** What a device sent, as the value's type, or null: never a guess. */
export function specValue(p: Prop, raw: unknown): boolean | number | string | null {
  if (raw === null || raw === undefined) return null;
  switch (p.type) {
    case "Boolean":
      if (typeof raw === "boolean") return raw;
      if (typeof raw === "number") return raw !== 0;
      return raw === "on" ? true : raw === "off" ? false : null;
    case "Enum":
      if (typeof raw === "number") return p.names?.get(raw) ?? String(raw);
      return typeof raw === "string" ? raw.slice(0, MAX_TEXT) : null;
    case "Integer":
    case "Number": {
      const n = typeof raw === "string" && raw !== "" ? Number(raw) : raw; // older devices send numbers as text
      if (typeof n !== "number" || !Number.isFinite(n)) return null;
      if (p.type === "Number") return Number(n.toFixed(p.digits ?? 4));
      return p.legacy ? Math.round(n) : Math.round(n * 10 ** p.scale); // an older device's reading is already in its units
    }
    default:
      if (p.colour === "hex") {
        const n = Number(raw);
        return Number.isFinite(n) ? `#${(Math.trunc(n) & 0xffffff).toString(16).padStart(6, "0")}` : null;
      }
      return (typeof raw === "object" ? JSON.stringify(raw) : String(raw)).slice(0, MAX_TEXT);
  }
}

/** A 24-bit colour's hue (0 to 360) and saturation (0 to 100). Brightness is its own property on these lights. */
export function hueAndSaturation(rgb: number): { hue: number; saturation: number } {
  const [r, g, b] = [(rgb >> 16) & 255, (rgb >> 8) & 255, rgb & 255];
  const max = Math.max(r, g, b);
  const span = max - Math.min(r, g, b);
  if (span === 0) return { hue: 0, saturation: 0 };
  const sector = max === r ? ((g - b) / span + 6) % 6 : max === g ? (b - r) / span + 2 : (r - g) / span + 4;
  return { hue: Math.round(sector * 60), saturation: Math.round((span / max) * 100) };
}

/** The colour at full value, which is what a light turns into a colour at its own brightness. */
export function colourOf(hue: number, saturation: number): number {
  const channel = (offset: number) => {
    const k = (offset + hue / 60) % 6;
    return Math.round(255 * (1 - (saturation / 100) * Math.max(0, Math.min(k, 4 - k, 1))));
  };
  return (channel(5) << 16) | (channel(3) << 8) | channel(1);
}
