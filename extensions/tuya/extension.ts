// Smart Life and Tuya devices over the local network, protocols 3.1 to 3.5, written only on ctx.tcp and ctx.crypto.
// One TCP connection per entry in hosts: a gateway with its children, or a Wi-Fi device. Everything runs on one goroutine, so
// background.run serves each link in turn with short reads, and devices.poll only reports what it heard.
//
// Operations budget, limit 600 a minute. Links wait 200 ms each per run, so reads top out at 300 a minute from five links
// up, plus 6 heartbeats per link and one query per child after connecting. spendExtraRead keeps the total near 480.
import { kindOf } from "./categories";
import { COLOUR_PARTS, colourMax, formatColour, parseColour } from "./colour";
import { BAD_AUTH, CMD_HEARTBEAT, control, decode, dpsOf, hasSessionKey, negotiate, newSession, nextFrame, proveByRead, sendCommand, statusRead, type Frame, type Session } from "./protocol";
import { cleanMapping, type Mapping } from "./rules";
import { valuesOf, writable, readable, declare, colourPoint } from "./values";

const PORT = 6668;
const DIAL_MS = 2000;
const HANDSHAKE_MS = 5000;
const PROBE_MS = 1000;
const HEARTBEAT_MS = 10_000;
const SILENCE_MS = 30_000; // a silent socket means a dead link; devices answer every heartbeat
const RECONNECT_MS = 10_000;
const MIN_FAILING_MS = 60_000; // so a rebooting gateway doesn't cause a scan
const MIN_SWEEP_GAP_MS = 300_000; // so a device that's really off doesn't scan the network forever
const MAX_SWEEP_MS = 10_000;
const READ_MS = 200;
const DRAIN_MS = 20;
const MAX_READS = 6;
const OPS_TARGET_PER_MINUTE = 480;
const MAX_LINKS = 15; // the host allows 16 open connections, and looking for a moved device needs one

let extraReads = 0;
let extraReadsSince = 0;

function spendExtraRead(): boolean {
  const now = Date.now();
  if (now - extraReadsSince >= 60_000) {
    extraReads = 0;
    extraReadsSince = now;
  }
  const regular = Math.min(300, 60 * (links?.length ?? 1)) + 6 * (links?.length ?? 1);
  return ++extraReads <= Math.min(300, OPS_TARGET_PER_MINUTE - regular);
}
const settings = {
  devices: { label: "Devices file", type: "file", required: true, secretFields: ["key"], help: "The file `python -m tinytuya wizard` writes, inside your config folder, like secrets/tuya.json. It holds every device's local key." },
  hosts: { label: "Hosts", type: "string", required: true, help: "<device id>=<ip>@<version>, separated by commas, version 3.1 to 3.5. `python -m tinytuya scan` lists all three. A gateway carries its Zigbee and Bluetooth mesh devices. The ip and version may be left out if the devices file has them." },
} as const;

type Ctx = ExtensionContext<typeof settings>;

interface RawDevice {
  id: string;
  name: string;
  key: string; // must never leave this file
  category: string;
  product_name: string;
  node_id?: string;
  parent?: string; // the wizard's: the gateway's device id, or "" if it couldn't tell
  gateway_id?: string; // the same, from an older wizard
  ip?: string;
  version?: string;
  mapping: Record<string, Mapping>;
}

interface Link {
  root: RawDevice;
  children: RawDevice[];
  ids: string[];
  version: string;
  device22: boolean;
  up: boolean; // the peer proved it holds the key on this connection
  host: string;
  session: Session | null;
  byCid: Record<string, string>;
  nodeOf: Record<string, string>;
  pending: RawDevice[]; // sleeping battery sensors often miss this query; their next push fills them in
  heard: number;
  nextBeat: number;
  nextDial: number;
  failingSince: number;
  lastSweep: number;
}

let links: Link[] | null = null;
let devices: RawDevice[] = []; // in file order, only those reachable through a link
let unlinked: RawDevice[] = []; // the rest of the file, which no entry in hosts reaches
const linkOf: Record<string, Link> = {};
const current: Record<string, Record<string, unknown>> = {};
const online: Record<string, boolean> = {};

function setOnline(value: boolean, ids: string[]): void {
  for (const id of ids) online[id] = value;
}

/** Every other address on the /24 of a private IPv4 address, except those in use. */
function candidates(old: string, used: string[]): string[] {
  const parts = old.split(".").map(Number);
  const [a, b] = parts;
  const isPrivate = a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168);
  if (parts.length !== 4 || parts.some((n) => !Number.isInteger(n) || n < 0 || n > 255) || !isPrivate) return [];
  const out: string[] = [];
  for (let host = 1; host <= 254; host++) {
    const ip = `${a}.${b}.${parts[2]}.${host}`;
    if (ip !== old && !used.includes(ip)) out.push(ip);
  }
  return out;
}

function statusOf(link: Link, d: RawDevice): ReturnType<typeof statusRead> {
  return statusRead(link.version, link.device22, link.root.id, link.nodeOf[d.id], Object.keys(d.mapping));
}

/** Only a peer that holds the local key completes the handshake, or from 3.3 down answers a status read in a way that decrypts. */
function probe(ctx: Ctx, link: Link, ip: string): boolean {
  let conn: TcpConnection | null = null;
  try {
    conn = ctx.tcp.connect(ip, PORT, { timeoutMs: PROBE_MS });
    const s = newSession(ctx, conn, link.version, link.root.key);
    if (hasSessionKey(link.version)) negotiate(ctx, s, PROBE_MS);
    else proveByRead(ctx, s, statusOf(link, link.root), PROBE_MS);
    return true;
  } catch {
    return false;
  } finally {
    if (conn !== null) conn.close();
  }
}

// shortcut: the new address is only kept in memory, so after a restart the old one is tried first and the sweep repeats
// after the wait; keep it in ctx.storage if restarts with stale addresses become annoying.
function rediscover(ctx: Ctx, link: Link): void {
  // shortcut: 3.1 answers reads in plain text, so nothing there could prove who it is; it stays at its address, and hosts is how to move it.
  if (link.version === "3.1") return;
  const used = (links ?? []).map((l) => l.host); // these devices accept few connections, so a probe could disturb one
  const list = candidates(link.host, used);
  if (list.length === 0) return;
  const started = Date.now();
  for (const ip of ctx.tcp.scan(list, PORT, { timeoutMs: PROBE_MS })) {
    if (Date.now() - started > MAX_SWEEP_MS) return;
    if (probe(ctx, link, ip)) {
      ctx.log(`${link.root.name} found at a new address, ${ip}, was ${link.host}`);
      link.host = ip;
      return;
    }
  }
}

/** Drops the connection of a link whose devices were all removed. It connects again by itself when one comes back. */
function park(link: Link): void {
  if (link.session !== null) link.session.conn.close();
  link.session = null;
  link.up = false;
  if (link.nextDial !== Infinity) link.nextDial = 0; // Infinity is a link there's no room for
  setOnline(false, link.ids);
}

/**
 * Every /24 with a link on it, for anything taking connections on Tuya's port. Which device that is can't be told without
 * its key, and connected devices are left out, since they accept few connections. A connect sends nothing.
 */
function discover(ctx: Ctx): FoundDevice[] {
  const list = start(ctx);
  const connected = list.filter((l) => l.session !== null).map((l) => l.host);
  const out: FoundDevice[] = [];
  for (const d of devices) {
    const link = linkOf[d.id];
    if (link.session !== null) out.push({ id: d.id, name: d.name, product: d.product_name, address: link.host });
  }
  const subnets = new Set(list.map((l) => l.host).filter((h) => candidates(h, []).length > 0).map((h) => h.replace(/\.\d+$/, "")));
  const open: string[] = [];
  for (const subnet of subnets) {
    const hosts: string[] = [];
    for (let host = 1; host <= 254; host++) if (!connected.includes(`${subnet}.${host}`)) hosts.push(`${subnet}.${host}`);
    open.push(...ctx.tcp.scan(hosts, PORT, { timeoutMs: PROBE_MS }));
  }
  for (const ip of open) {
    const parked = list.find((l) => l.host === ip);
    if (parked !== undefined) {
      for (const id of parked.ids) {
        const d = devices.find((x) => x.id === id);
        if (d !== undefined) out.push({ id, name: d.name, product: d.product_name, address: ip });
      }
    } else {
      out.push({ id: `address-${ip}`, name: "Unknown Smart Life device", address: ip, note: "Something answers on Tuya's port here, but your devices file doesn't say what. Pair it in Smart Life if it isn't yet, run `python -m tinytuya wizard` again, and add it to Hosts." });
    }
  }
  for (const d of unlinked) {
    out.push({ id: d.id, name: d.name, product: d.product_name, note: "It's in your devices file, but no entry in Hosts reaches it. Add it there, or the gateway it's behind." });
  }
  return out;
}

function linkFailed(link: Link): boolean {
  const now = Date.now();
  if (link.failingSince === 0) link.failingSince = now;
  if (now - link.failingSince < MIN_FAILING_MS || (link.lastSweep !== 0 && now - link.lastSweep < MIN_SWEEP_GAP_MS)) return false;
  link.lastSweep = now;
  return true;
}

function linkDown(ctx: Ctx, link: Link, reason: unknown): void {
  if (link.session !== null) link.session.conn.close();
  link.session = null;
  link.up = false;
  link.nextDial = Date.now() + RECONNECT_MS;
  setOnline(false, link.ids);
  ctx.refreshDevices();
  ctx.log(`${link.root.name}: link down, reconnecting: ${reason}`);
  if (linkFailed(link)) {
    try {
      rediscover(ctx, link);
    } catch (e) {
      ctx.log(`${link.root.name}: looking for it on the network failed: ${e}`);
    }
  }
}

function connect(ctx: Ctx, link: Link): void {
  let conn: TcpConnection | null = null;
  try {
    conn = ctx.tcp.connect(link.host, PORT, { timeoutMs: DIAL_MS });
    const s = newSession(ctx, conn, link.version, link.root.key);
    if (hasSessionKey(link.version)) negotiate(ctx, s, HANDSHAKE_MS);
    sendCommand(ctx, s, statusOf(link, link.root));
    link.session = s;
  } catch (e) {
    if (conn !== null) conn.close();
    linkDown(ctx, link, e);
    return;
  }
  const now = Date.now();
  link.pending = link.children.slice();
  link.heard = now;
  link.nextBeat = now + HEARTBEAT_MS;
  if (link.session.proven) proven(ctx, link);
}

/** From 3.3 down this waits for the first message that decrypts, so a wrong key stays offline. */
function proven(ctx: Ctx, link: Link): void {
  link.up = true;
  link.failingSince = 0;
  online[link.root.id] = true;
  // Battery sensors sleep, so they may not answer for hours. One that reported before is still there.
  // shortcut: this memory is lost on a restart, so they show offline until their next report; keep it in ctx.storage if that annoys.
  for (const c of link.children) online[c.id] = current[c.id] !== undefined;
  ctx.refreshDevices();
  ctx.log(`${link.root.name} connected at ${link.host}`);
}

function handleFrame(ctx: Ctx, link: Link, s: Session, frame: Frame): void {
  let msg: any;
  try {
    msg = decode(ctx, s, frame);
  } catch (e) {
    ctx.log(`${link.root.name}: bad message: ${e}`);
    return;
  }
  if (s.proven && !link.up) proven(ctx, link);
  if (s.unvalid && !link.device22) { // it needs the other kind of status read
    link.device22 = true;
    link.pending = link.children.slice();
    sendCommand(ctx, s, statusOf(link, link.root));
  }
  if (msg === null || typeof msg !== "object") return;
  const { dps, cid } = dpsOf(msg);
  if (dps === undefined || Object.keys(dps).length === 0) return;
  let id = link.root.id;
  if (cid !== "") {
    if (link.byCid[cid] === undefined) { // paired after the devices file was fetched
      ctx.log(`${link.root.name}: unknown sub-device ${cid}`);
      return;
    }
    id = link.byCid[cid];
  }
  current[id] = { ...current[id], ...dps };
  online[id] = true;
  ctx.refreshDevices();
}

function serve(ctx: Ctx, link: Link, s: Session): void {
  for (let reads = 0; reads < MAX_READS; reads++) {
    // Over the host's 600 operations a minute the extension is stopped, so a chatty gateway waits for the next run.
    if (reads > 0 && !spendExtraRead()) break;
    const chunk = s.conn.read({ timeoutMs: reads === 0 ? READ_MS : DRAIN_MS });
    if (chunk === "") break;
    s.buffer += chunk;
    for (;;) {
      let frame: Frame | null;
      try {
        frame = nextFrame(ctx, s);
      } catch (e) {
        if (String(e).includes(BAD_AUTH)) {
          ctx.log(`${link.root.name}: frame rejected`);
          continue;
        }
        throw e;
      }
      if (frame === null) break;
      link.heard = Date.now();
      handleFrame(ctx, link, s, frame);
    }
  }
  const now = Date.now();
  if (now - link.heard > SILENCE_MS) throw new Error(`no data for ${SILENCE_MS / 1000} seconds`);
  if (!s.proven && now - s.started > SILENCE_MS) throw new Error(`nothing readable for ${SILENCE_MS / 1000} seconds: is the local key or the version wrong?`);
  const child = link.pending.shift();
  if (child !== undefined) sendCommand(ctx, s, statusOf(link, child));
  if (now >= link.nextBeat) {
    link.nextBeat = now + HEARTBEAT_MS;
    sendCommand(ctx, s, { cmd: CMD_HEARTBEAT, body: { gwId: link.root.id, devId: link.root.id } });
  }
}

/** The gateway each sub-device hangs off. A gateway only knows its own children, so a wrong guess leaves one unreachable. */
function assignChildren(all: RawDevice[], made: Link[]): void {
  const byId: Record<string, Link> = {};
  for (const l of made) byId[l.root.id] = l;
  const gateways = made.filter((l) => l.root.category.startsWith("wg"));
  for (const d of all) {
    if (!d.node_id || byId[d.id] !== undefined) continue;
    // By the file's parent, else the wizard's own clue that a sub-device shares its gateway's key, else any gateway.
    // shortcut: with two gateways, no parent and unrelated keys everything goes to the first one; fetch the file again with a newer wizard.
    const link = byId[d.parent ?? ""] ?? byId[d.gateway_id ?? ""] ?? gateways.find((l) => l.root.key === d.key) ?? gateways[0];
    if (link === undefined) continue;
    link.children.push(d);
    link.ids.push(d.id);
    link.byCid[d.node_id] = d.id;
    link.nodeOf[d.id] = d.node_id;
  }
}

function start(ctx: Ctx): Link[] {
  if (links !== null) return links;
  const parsed: unknown = JSON.parse(ctx.settings.devices);
  if (!Array.isArray(parsed)) throw new Error("the devices file must hold a list of devices");
  const all: RawDevice[] = parsed.filter((d) => d !== null && typeof d === "object" && typeof d.id === "string" && d.id !== "").map((d) => {
    const category = typeof d.category === "string" ? d.category : "";
    return { ...d, category, name: d.name || d.product_name || d.id, mapping: cleanMapping(category, d.mapping) };
  });
  const made: Link[] = [];
  for (const part of ctx.settings.hosts.split(",")) {
    const entry = part.trim();
    if (entry === "") continue;
    const match = /^([^=@]+)(?:=([^@]*))?(?:@(.*))?$/.exec(entry);
    if (match === null) throw new Error(`bad host entry "${entry}", want id=ip@version`);
    const id = match[1].trim();
    const root = all.find((d) => d.id === id);
    if (root === undefined) throw new Error(`host ${id} is not in the devices file`);
    const host = match[2]?.trim() || root.ip;
    const version = match[3]?.trim() || root.version;
    if (!host || !version) throw new Error(`host ${id} needs an address and a version, and the devices file has none`);
    if (!/^3\.[1-5]$/.test(version)) throw new Error(`protocol ${version} is not supported, only 3.1 to 3.5`);
    const link: Link = { root, children: [], ids: [id], version, device22: version === "3.2", up: false, host, session: null, byCid: {}, nodeOf: {}, pending: [], heard: 0, nextBeat: 0, nextDial: 0, failingSince: 0, lastSweep: 0 };
    // shortcut: over MAX_LINKS the rest show as offline, since the host has no more connections; Wi-Fi devices past that need the service-bridge route.
    if (made.length >= MAX_LINKS) {
      link.nextDial = Infinity;
      ctx.log(`${root.name}: only ${MAX_LINKS} direct connections fit, so this one won't connect. Devices behind a gateway don't count.`);
    }
    made.push(link);
  }
  assignChildren(all, made);
  for (const link of made) for (const i of link.ids) linkOf[i] = link;
  devices = all.filter((d) => linkOf[d.id] !== undefined);
  unlinked = all.filter((d) => linkOf[d.id] === undefined);
  links = made;
  return made;
}

/** colour_hue, colour_saturation and colour_brightness, unknown and not writable until the light has said which form it uses. */
function colourValues(d: RawDevice): DeviceValue[] {
  const point = colourPoint(d);
  if (point === undefined) return [];
  const colour = parseColour(current[d.id]?.[point.dp]);
  const max = colour === null ? 1000 : colourMax(colour, point.m.code, valuesOf(point.m));
  return Object.keys(COLOUR_PARTS).map((code): DeviceValue => ({
    code, type: "Integer", writable: colour !== null, value: colour === null ? null : colour[COLOUR_PARTS[code]], min: 0, max: code === "colour_hue" ? 360 : max,
  }));
}

/** The data points to send for a write to a colour part, in the form the light last sent, in colour mode as TinyTuya's set_colour does. */
function colourWrite(d: RawDevice, code: string, value: number): Record<string, unknown> {
  const point = colourPoint(d);
  const colour = point === undefined ? null : parseColour(current[d.id]?.[point.dp]);
  if (point === undefined || colour === null) throw new Error(`${d.name} hasn't reported its colour yet`);
  const dps: Record<string, unknown> = { [point.dp]: formatColour({ ...colour, [COLOUR_PARTS[code]]: value }) };
  const mode = Object.keys(d.mapping).find((k) => d.mapping[k].code === "work_mode");
  if (mode !== undefined && valuesOf(d.mapping[mode]).range?.includes("colour")) dps[mode] = "colour";
  return dps;
}

defineExtension({
  id: "tuya",
  name: "Smart Life",
  version: "1.2.2",
  license: "MIT",
  description: "Tuya Zigbee and Wi-Fi devices paired in Smart Life or Tuya Smart, reached directly on your network.",
  categories: ["climate", "lighting", "energy", "sensors", "security"],
  tags: ["Tuya", "Smart Life", "Tuya Smart", "Zigbee", "plug", "thermostat", "bulb", "sensor"],
  // lan, because device addresses come from a list, and to look for a device that moved.
  access: { lan: true, sockets: true },
  settings,
  background: {
    everySeconds: 1,
    run(ctx) {
      const now = Date.now();
      const removed = ctx.removedDevices();
      for (const link of start(ctx)) {
        if (link.ids.every((id) => removed.includes(id))) {
          park(link);
          continue;
        }
        if (link.session === null) {
          if (now >= link.nextDial) connect(ctx, link);
          continue;
        }
        try {
          serve(ctx, link, link.session);
        } catch (e) {
          linkDown(ctx, link, e);
        }
      }
    },
  },
  devices: {
    everySeconds: 2,
    poll(ctx) {
      start(ctx);
      return devices.map((d): ExtensionDevice => ({
        id: d.id,
        name: d.name,
        kind: kindOf(d.category),
        product: d.product_name,
        online: online[d.id] === true,
        values: [...Object.keys(d.mapping).sort().map((dp) => declare(d, d.mapping[dp], current[d.id]?.[dp])), ...colourValues(d)],
      }));
    },
    discover,
    write(ctx, deviceId, code, value) {
      start(ctx);
      const link = linkOf[deviceId];
      const d = devices.find((x) => x.id === deviceId);
      if (link === undefined || d === undefined) throw new Error(`unknown device ${deviceId}`);
      let dps: Record<string, unknown>;
      if (COLOUR_PARTS[code] !== undefined && typeof value === "number") {
        dps = colourWrite(d, code, value);
      } else {
        const dp = Object.keys(d.mapping).find((k) => d.mapping[k].code === code);
        if (dp === undefined) throw new Error(`${d.name} has no value called ${code}`);
        if (!writable(d, d.mapping[dp], current[d.id]?.[dp])) throw new Error(`${code} is not writable`);
        dps = { [dp]: value };
      }
      if (link.session === null || !link.up) throw new Error(`${d.name} isn't connected`);
      try {
        sendCommand(ctx, link.session, control(link.version, link.root.id, link.nodeOf[deviceId], dps));
      } catch (e) {
        linkDown(ctx, link, e);
        throw e;
      }
      // So a saturation written straight after a hue builds on the new hue, not the colour the light last reported.
      if (COLOUR_PARTS[code] !== undefined) current[deviceId] = { ...current[deviceId], ...dps };
    },
  },
});
