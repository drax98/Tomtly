// Xiaomi and Roborock Wi-Fi devices over miIO, the encrypted UDP protocol they speak on port 54321.
// They don't push changes, so each one is polled. All of it runs on one goroutine.
import { deviceType, kindOf } from "./kinds";
import { familyFor } from "./legacy";
import { deriveKey, Entry, HELLO, MIOT_BATCH, NotMiot, OPS_PER_MINUTE, parseHello, POLL_OPS, PORT, spend, used, Value } from "./miio";
import { isLost, read, writeProp } from "./drivers";
import { miotModels, Prop, reading, roborockModels, roborockProps } from "./models";
import { MAX_TEXT, specProps, SpecService } from "./spec";
import { cachedSpec, fetchSpecs, specMissing, validUrn } from "./speccache";

const OFFLINE_RETRY_MS = 30_000; // so a dead device, which costs a full timeout, doesn't delay the others every poll
const POLL_BUDGET_MS = 30_000; // the host stops any call at 60 seconds, which would take every device offline
const MIN_FAILING_MS = 60_000; // before looking for a device elsewhere
const MIN_SWEEP_GAP_MS = 5 * 60_000;
const SWEEP_TIMEOUT_MS = 1000;
const SWEEP_OPS = 32; // a /24, counted the host's way
const MAX_OUTPUT = 900 * 1024; // the service refuses a list over 1 MiB, and then every device goes offline
const ROBOROCK = /^(roborock|rockrobo)\.vacuum\./;
const WAITING = "Waiting for its description from miot-spec.org. It's tried again on its own.";
const NOT_ON_SERVER = "miot-spec.org has no description of this model, so it can't be read. It's asked again every few hours.";

const settings = {
  devices: {
    label: "Devices file",
    type: "file",
    required: true,
    secretFields: ["token"],
    help: "Inside your config folder, like secrets/xiaomi.json: the output of Xiaomi-cloud-tokens-extractor. It holds every device's token.",
  },
} as const;

type Ctx = ExtensionContext<typeof settings>;

const NUMERIC_DID = /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/; // any other did is a kind of device miIO can't reach, such as Bluetooth

let entries: Entry[] | null = null;
let cursor = 0; // the next device to read, so devices that wait their turn in a big home always get one
/** Why a device in the file can't be reached over the LAN, in words for its card, or "" if it can. */
function unreachable(did: string, ip: unknown, token: unknown): string {
  const lanOnly = "This integration only uses your local network, not the Xiaomi cloud.";
  if (/^blt\./.test(did)) return `Bluetooth or Mesh, so it has no local network connection. ${lanOnly}`;
  if (/^lumi\./.test(did)) return `Zigbee: it answers only through its gateway or the cloud. ${lanOnly}`;
  if (/^group\./.test(did)) return "A group made in Mi Home, not a device.";
  if (!NUMERIC_DID.test(did)) return `Not a Wi-Fi device, so it can't be reached directly. ${lanOnly}`;
  if (!ip || !token) return `The devices file has no local address or token for it, which is usual for a device behind a gateway. ${lanOnly}`;
  if (!isPrivate(String(ip))) return "The devices file gives it an address outside your local network.";
  if (!/^[0-9a-fA-F]+$/.test(String(token)) || String(token).length % 2 !== 0) return "Its token in the devices file isn't hex.";
  return "";
}

/** Flattens the extractor's output. Every device in it gets a card, one that can't be read saying why. */
function load(ctx: Ctx): Entry[] {
  let servers: any;
  try {
    servers = JSON.parse(ctx.settings.devices);
  } catch {
    throw new Error("the devices file isn't valid JSON"); // the parser's message can quote the file
  }
  if (!Array.isArray(servers)) throw new Error("the devices file should be a list, as Xiaomi-cloud-tokens-extractor writes it");
  const out: Entry[] = [];
  const seen = new Set<string>();
  const text = (v: unknown): v is string | undefined => v === undefined || typeof v === "string";
  for (const server of servers) {
    for (const home of server?.homes ?? []) {
      for (const d of home?.devices ?? []) {
        const did = typeof d?.did === "number" || typeof d?.did === "string" ? String(d.did) : "";
        if (!/^[\w.:-]{1,80}$/.test(did) || !text(d.name) || !text(d.model) || !text(d.localip) || !text(d.token)) {
          ctx.log("skipping an unreadable device entry");
          continue;
        }
        if (seen.has(did)) continue;
        seen.add(did);
        const model: string = d.model ?? "";
        const table = Object.prototype.hasOwnProperty.call(miotModels, model) ? miotModels[model] : undefined;
        const roborock = !table && ROBOROCK.test(model);
        const family = familyFor(model);
        const urn = validUrn(d.spec_type) ? d.spec_type : "";
        const why = unreachable(did, d.localip, d.token);
        // The hand tables' models keep refusing a file they can't use, as they always have.
        if ((table || roborockModels[model] === true) && why.indexOf("hex") >= 0) throw new Error(`the token for ${d.name || model} isn't hex`);
        const e: Entry = {
          id: did, name: d.name || model || did, model, kind: table ? table.kind : roborock ? "vacuum" : urn ? kindOf(deviceType(urn)) : family ? family.kind : "other",
          props: table ? table.props : roborock ? roborockPropsFor(model) : [], roborock,
          driver: table ? "table" : roborock ? "roborock" : urn ? "spec" : family ? "legacy" : "none", urn, note: "",
          batch: MIOT_BATCH, answered: false, resume: 0, hidden: new Set(), strikes: new Map(),
          ip: d.localip ?? "", key: { token: "", aes: "", iv: "" },
          // Devices drop a repeated id, so a restart must not start counting from the same number.
          nextId: parseInt(ctx.crypto.random(4), 16) % (1 << 30),
          values: {}, online: false, everTried: false, nextTry: 0, failingSince: 0, lastSweep: 0,
        };
        if (e.driver === "legacy" && family) e.props = family.props;
        if (e.driver === "none") e.note = `The devices file has no spec_type for it and it isn't a known older model, so it can't be read. Fetching the file again with a newer extractor may help.`;
        if (why) {
          e.driver = "none";
          e.note = why;
          e.props = [];
        } else if (e.driver !== "none") {
          e.key = deriveKey(ctx, d.token.toLowerCase());
        }
        out.push(e);
      }
    }
  }
  return out;
}

/** Other Roborocks share the commands but not the fan speed numbers, which change by generation, so that one is only shown. */
function roborockPropsFor(model: string): Prop[] {
  if (roborockModels[model] === true) return roborockProps;
  return roborockProps.map((p) => (p.code === "fan_speed" ? reading("fan_speed", 0, 1, "") : p));
}

function ensureLoaded(ctx: Ctx): Entry[] {
  if (entries === null) entries = load(ctx);
  return entries;
}

/**
 * The host counts asking for any other address as a violation, and stops an extension after ten, so a file with a public
 * address on one device mustn't get that far. 127 only happens in tests: the host refuses loopback for a running extension.
 * shortcut: a real file saying 127.0.0.1 would still earn violations, take 127 out if a test double replaces it.
 */
function isPrivate(ip: string): boolean {
  const m = /^(\d+)\.(\d+)\.\d+\.\d+$/.exec(ip);
  if (!m) return false;
  const [a, b] = [Number(m[1]), Number(m[2])];
  return a === 10 || a === 127 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
}

/** The /24 around a private address, or nothing for any other address. */
function neighbours(ip: string): string[] {
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(ip);
  if (!m || !isPrivate(ip)) return [];
  const [a, b, c] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const out: string[] = [];
  for (let host = 1; host <= 254; host++) {
    const candidate = `${a}.${b}.${c}.${host}`;
    if (candidate !== ip) out.push(candidate);
  }
  return out;
}

/** Asks every /24 the devices file has an address on who's there. The hello needs no token and changes nothing. */
function discover(ctx: Ctx): FoundDevice[] {
  const list = ensureLoaded(ctx);
  const subnets = new Set(list.filter((e) => isPrivate(e.ip)).map((e) => e.ip.replace(/\.\d+$/, "")));
  const out: FoundDevice[] = [];
  for (const subnet of subnets) {
    const addresses: string[] = [];
    for (let host = 1; host <= 254; host++) addresses.push(`${subnet}.${host}`);
    spend(SWEEP_OPS);
    for (const answer of ctx.udp.scan(addresses, PORT, HELLO, { timeoutMs: SWEEP_TIMEOUT_MS })) {
      let id: string;
      try {
        id = String(parseHello(answer.data).id);
      } catch {
        continue; // something else on that port
      }
      const e = list.find((x) => x.id === id);
      out.push(e
        ? { id, name: e.name, product: e.model, address: answer.from }
        : { id, name: "Unknown Xiaomi device", address: answer.from, note: "It isn't in your devices file, so its token isn't known. Pair it in Mi Home if it isn't yet, then fetch the file again with Xiaomi-cloud-tokens-extractor." });
    }
  }
  return out;
}

/** Whether to look for the device now. Counts the sweep if so. */
function maySweep(e: Entry, now: number): boolean {
  if (e.failingSince === 0) e.failingSince = now;
  if (now - e.failingSince < MIN_FAILING_MS || (e.lastSweep !== 0 && now - e.lastSweep < MIN_SWEEP_GAP_MS)) return false;
  e.lastSweep = now;
  return true;
}

/**
 * Looks for the device on its /24 and adopts an address only when the id there matches and a read that needs the token
 * succeeds, because the id in a header is public and anyone can copy it. Never writes.
 * shortcut: the address is only kept in memory, so after a restart the old one is tried first; persist it with
 * access.storage if restarts with stale addresses become annoying.
 */
function find(ctx: Ctx, e: Entry): Record<string, Value> | null {
  // miIO's header holds a uint32, so any other did can't be recognised.
  const id = /^\d+$/.test(e.id) ? Number(e.id) : -1;
  if (id < 0 || id > 0xffffffff) return null;
  const addresses = neighbours(e.ip);
  spend(Math.ceil(addresses.length / 8));
  for (const answer of ctx.udp.scan(addresses, PORT, HELLO, { timeoutMs: SWEEP_TIMEOUT_MS })) {
    try {
      if (parseHello(answer.data).id !== id) continue;
      const values = read(ctx, e, answer.from);
      ctx.log(`${e.name} found at a new address: ${e.ip} is now ${answer.from}`);
      e.ip = answer.from;
      return values;
    } catch {
      // not this one
    }
  }
  return null;
}

/** A device with a spec that won't take MIoT calls, as some older models do: its family's commands, if it has one. */
function useFallback(ctx: Ctx, e: Entry): void {
  const family = familyFor(e.model);
  ctx.log(`${e.name} doesn't take MIoT reads${family ? ", using its older commands" : ""}`);
  e.driver = family ? "legacy" : "none";
  e.props = family ? family.props : [];
  e.note = family ? "" : "It answers on your network, but not to MIoT reads, and it isn't a known older model.";
}

function pollOne(ctx: Ctx, e: Entry): void {
  const now = Date.now();
  if (!e.online && e.everTried && now < e.nextTry) return;
  e.everTried = true;
  let values: Record<string, Value> | null = null;
  let failure: unknown;
  try {
    values = read(ctx, e, e.ip);
  } catch (error) {
    failure = error;
    if (error instanceof NotMiot) {
      useFallback(ctx, e);
      return;
    }
    if (used() + SWEEP_OPS <= OPS_PER_MINUTE && maySweep(e, now)) values = find(ctx, e);
  }
  if (values !== null) {
    e.failingSince = 0;
    Object.assign(e.values, values);
    e.online = true;
    return;
  }
  if (e.online) ctx.log(`${e.name} is offline: ${failure}`); // log the transition, not every poll
  e.online = false;
  e.nextTry = now + OFFLINE_RETRY_MS;
}

function adopt(ctx: Ctx, e: Entry, services: SpecService[]): void {
  e.props = specProps(e.urn, services);
  e.note = "";
  if (e.props.length > 0) return;
  useFallback(ctx, e);
  if (e.driver === "none") e.note = "Its description lists nothing that can be shown, so there's nothing to read. Many of these are Bluetooth devices.";
}

/** Gives devices their specs: what's already here at once, then a few requests. A failure only leaves those devices waiting. */
function loadSpecs(ctx: Ctx, list: Entry[], start: number): void {
  const waiting = () => list.filter((e) => e.driver === "spec" && e.props.length === 0);
  const fromCache = () => {
    for (const e of waiting()) {
      try {
        const services = cachedSpec(ctx, e.urn);
        if (services) adopt(ctx, e, services);
      } catch (error) {
        ctx.log(`${e.name}: a bad description: ${error}`);
      }
      if (e.driver === "spec" && e.props.length === 0) e.note = specMissing(e.urn) ? NOT_ON_SERVER : WAITING;
    }
  };
  fromCache();
  const urns = waiting().map((e) => e.urn).filter((urn, i, all) => all.indexOf(urn) === i);
  if (urns.length === 0) return;
  spend(fetchSpecs(ctx, urns, start, start + POLL_BUDGET_MS));
  fromCache();
}

/** As many devices as this poll's share allows, from where the last one stopped. */
function pollSome(ctx: Ctx, list: Entry[], start: number): void {
  const opsAtStart = used();
  const removed = ctx.removedDevices();
  for (let turns = 0; turns < list.length; turns++) {
    if (Date.now() - start > POLL_BUDGET_MS || used() - opsAtStart >= POLL_OPS || used() >= OPS_PER_MINUTE) return; // the rest keep what they showed
    const e = list[cursor % list.length];
    cursor = (cursor + 1) % list.length;
    if (e.driver !== "none" && e.props.length > 0 && !removed.includes(e.id)) pollOne(ctx, e);
  }
}

function deviceOf(e: Entry): ExtensionDevice {
  const values = e.props.filter((p) => !e.hidden.has(p.did)).map((p) => {
    const v: DeviceValue = { code: p.code, type: p.type, writable: p.writable, value: e.values[p.code] ?? null, scale: p.scale };
    if (p.range) v.range = p.range;
    if (p.min !== undefined) v.min = p.min;
    if (p.max !== undefined) v.max = p.max;
    if (p.step !== undefined) v.step = p.step;
    if (p.unit) v.unit = p.unit;
    return v;
  });
  if (e.note) values.push({ code: "unavailable", type: "String", value: e.note.slice(0, MAX_TEXT * 2) });
  return { id: e.id, name: e.name, kind: e.kind, product: e.model, online: e.online && !e.note, values };
}

/**
 * The whole list is refused over 1 MiB, so past that the longest devices give up their values and say so.
 * shortcut: which device loses them can change between polls, it takes over 100 devices with the longest specs to get here.
 */
function fit(ctx: Ctx, devices: ExtensionDevice[]): ExtensionDevice[] {
  const sizes = devices.map((d) => ctx.crypto.hex(JSON.stringify(d)).length / 2);
  let total = sizes.reduce((sum, size) => sum + size, 0);
  while (total > MAX_OUTPUT) {
    const longest = sizes.indexOf(Math.max(...sizes));
    const note = "This home has more device values than one list can hold, so this device's are left out.";
    devices[longest] = { ...devices[longest], online: false, values: [{ code: "unavailable", type: "String", value: note }] };
    total -= sizes[longest] - 300;
    sizes[longest] = 300;
  }
  return devices;
}

defineExtension({
  id: "xiaomi",
  name: "Xiaomi Home",
  version: "1.3.2",
  license: "MIT",
  description: "Any Xiaomi or Roborock device paired in Mi Home, reached directly on your network. The first time it sees a model, it asks Xiaomi's public miot-spec.org for that model's description.",
  categories: ["cleaning", "climate", "lighting", "sensors"],
  tags: ["Xiaomi", "Mi Home", "Roborock", "Mijia", "vacuum", "air purifier", "humidifier", "lamp"],
  access: { lan: true, sockets: true, network: ["miot-spec.org"], storage: true },
  settings,
  devices: {
    everySeconds: 10,
    poll(ctx) {
      const list = ensureLoaded(ctx);
      const start = Date.now();
      loadSpecs(ctx, list, start);
      pollSome(ctx, list, start);
      return fit(ctx, list.map(deviceOf));
    },
    discover,
    write(ctx, deviceId, code, value) {
      const e = ensureLoaded(ctx).find((x) => x.id === deviceId);
      if (!e) throw new Error(`unknown device ${deviceId}`);
      const p = e.props.find((x) => x.code === code);
      if (!p || !p.writable || e.hidden.has(p.did)) throw new Error(`${code} is not writable`);
      for (let attempt = 1; ; attempt++) {
        try {
          writeProp(ctx, e, p, value);
          return;
        } catch (error) {
          // A toggle whose reply got lost may have worked, and repeating it would undo that.
          if (attempt >= 2 || !isLost(error) || (p.type === "Action" && !e.roborock)) throw error;
        }
      }
    },
  },
});
