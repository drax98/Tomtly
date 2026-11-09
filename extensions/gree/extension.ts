// Gree-based air conditioners (Gree, Sinclair, EWPE Smart), over the unit's local protocol on UDP port 7000.
const PORT = 7000;
const ECB_KEY = "a3K8Bx%2r8Y7#xDh"; // the published generic key every unit answers scan and bind with
const GCM_KEY = "{yxAHAY_Lm6pbC/<";
const GCM_AAD = "qualcomm-test";
const GCM_NONCE = "5440784449675a516c5e6313";
// Each list's index is the protocol value, per https://github.com/tomikaa87/gree-remote
const MODES = ["auto", "cool", "dry", "fan", "heat"];
const FAN_SPEEDS = ["auto", "low", "medium_low", "medium", "medium_high", "high"];
const SWING_VERTICAL = ["default", "full", "top", "upper", "middle", "lower", "bottom", "swing_bottom", "swing_lower", "swing_middle", "swing_upper", "swing_top"];
const SWING_HORIZONTAL = ["default", "full", "left", "left_middle", "middle", "right_middle", "right"];
const NAMES: Record<string, string[]> = { mode: MODES, fan_speed: FAN_SPEEDS, swing_vertical: SWING_VERTICAL, swing_horizontal: SWING_HORIZONTAL };

// On or off, and shown only if the unit reports them.
const SWITCHES = [
  { col: "Air", code: "fresh_air" },
  { col: "Blo", code: "x_fan" }, // keeps the fan running after shutdown, to dry the unit
  { col: "Health", code: "health" },
  { col: "SwhSlp", code: "sleep" },
  { col: "StHt", code: "frost_protection" }, // holds the room at 8 °C
  { col: "SvSt", code: "energy_saving" },
];

// code is what scripts and the page see, col is the protocol's name for it. This is an allowlist.
// TemUn stays out: everything here assumes Celsius, and temp_set forces it. So do TemRec and HeatCoolType, which aren't settings.
const FIELDS = [
  { col: "Pow", code: "power" },
  { col: "Mod", code: "mode" },
  { col: "SetTem", code: "temp_set" },
  { col: "WdSpd", code: "fan_speed" },
  { col: "Quiet", code: "quiet" },
  { col: "Tur", code: "turbo" },
  { col: "Lig", code: "light" },
  // The unit stores it, so it mutes the remote and the Gree app too. Turning it back on beeps once.
  { col: "Buzzer_ON_OFF", code: "beep" },
  { col: "TemSen", code: "temp_current" },
  { col: "SwUpDn", code: "swing_vertical" },
  { col: "SwingLfRig", code: "swing_horizontal" },
  ...SWITCHES,
];

interface Session {
  mac: string;
  key: string; // the unit's own key, from bind. Only ever held in memory.
  gcm: boolean;
}
let session: Session | null = null;
let address = ""; // where the unit is now, which starts at the setting and moves if the unit is found elsewhere
let knownMac = "";
let failingSince = 0;
let lastSweep = 0;

// A DHCP change is worth looking for only once it isn't a blip, and a look can't run constantly.
const SWEEP_AFTER_MS = 60_000;
const SWEEP_EVERY_MS = 5 * 60_000;

type Ctx = ExtensionContext<typeof settings>;

const settings = {
  host: { label: "Air conditioner address", type: "string", required: true, help: "Its IP address, without a port. Reserve it on your router so it doesn't change." },
  name: { label: "Name", type: "string", default: "Air conditioner" },
  mac: { label: "MAC address", type: "string", help: "Optional, like aabbccddeeff. Lets the unit be found again after an address change without guessing." },
} as const;

function isTimeout(e: unknown): boolean {
  return /timeout/i.test(String(e));
}

/** Sends one datagram and returns the reply as text. Only a lost packet is retried, and `tries` says how often. */
function ask(ctx: Ctx, request: object, tries: number, timeoutMs: number): any {
  const data = ctx.crypto.hex(JSON.stringify(request));
  for (let i = 1; ; i++) {
    try {
      return JSON.parse(ctx.crypto.text(ctx.udp.request(address || ctx.settings.host, PORT, data, { timeoutMs })));
    } catch (e) {
      if (!isTimeout(e) || i >= tries) throw e;
    }
  }
}

function seal(ctx: Ctx, key: string, gcm: boolean, body: object): { pack: string; tag?: string } {
  const data = ctx.crypto.hex(JSON.stringify(body));
  if (!gcm) {
    return { pack: ctx.crypto.toBase64(ctx.crypto.encrypt({ mode: "ecb", key: ctx.crypto.hex(key), data, padding: true })) };
  }
  const out = ctx.crypto.encrypt({ mode: "gcm", key: ctx.crypto.hex(key), iv: GCM_NONCE, aad: ctx.crypto.hex(GCM_AAD), data });
  // The sealed bytes end in a 16-byte tag, which the protocol sends separately.
  return { pack: ctx.crypto.toBase64(out.slice(0, -32)), tag: ctx.crypto.toBase64(out.slice(-32)) };
}

function unseal(ctx: Ctx, key: string, envelope: { pack?: string; tag?: string }): any {
  if (typeof envelope.pack !== "string") throw new Error("the air conditioner sent a reply with no data");
  const aes = ctx.crypto.hex(key);
  const text = envelope.tag
    ? ctx.crypto.decrypt({ mode: "gcm", key: aes, iv: GCM_NONCE, aad: ctx.crypto.hex(GCM_AAD), data: ctx.crypto.fromBase64(envelope.pack) + ctx.crypto.fromBase64(envelope.tag) })
    : ctx.crypto.decrypt({ mode: "ecb", key: aes, data: ctx.crypto.fromBase64(envelope.pack), padding: true });
  return JSON.parse(ctx.crypto.text(text));
}

/** One encrypted request and its decrypted reply. */
function exchange(ctx: Ctx, s: Session, body: object, i: number, tries: number, timeoutMs: number): any {
  const sealed = seal(ctx, s.key, s.gcm, body);
  const reply = ask(ctx, { t: "pack", i, uid: 0, cid: "app", tcid: s.mac, ...sealed }, tries, timeoutMs);
  return unseal(ctx, s.key, reply);
}

/** Scans the unit for its MAC, then binds with the generic key to get the key it uses from now on. Bind changes nothing on the unit. */
function connect(ctx: Ctx): Session {
  const scan = ask(ctx, { t: "scan" }, 3, 1500);
  const found = unseal(ctx, scan.tag ? GCM_KEY : ECB_KEY, scan);
  if (typeof found.mac !== "string" || found.mac === "") throw new Error("the air conditioner's scan reply has no MAC");
  knownMac = found.mac;

  // A unit speaks one of two schemes and stays silent to the other, so try each in turn, the scan's own first.
  for (const gcm of scan.tag ? [true, false] : [false, true]) {
    const generic: Session = { mac: found.mac, key: gcm ? GCM_KEY : ECB_KEY, gcm };
    const bind: Record<string, unknown> = { t: "bind", mac: found.mac, uid: 0 };
    if (gcm) bind.cid = found.mac;
    let reply: any;
    try {
      reply = exchange(ctx, generic, bind, 1, 2, 1500);
    } catch (e) {
      if (isTimeout(e) || /decrypt|message authentication|padding|JSON/i.test(String(e))) continue;
      throw e;
    }
    if (reply.t === "bindok" && typeof reply.key === "string" && reply.key !== "") {
      return { mac: found.mac, key: reply.key, gcm };
    }
  }
  throw new Error("the air conditioner didn't accept a bind: is another app pairing with it?");
}

function status(ctx: Ctx, s: Session): Record<string, number> {
  const cols = FIELDS.map((f) => f.col);
  const reply = exchange(ctx, s, { t: "status", mac: s.mac, cols }, 0, 3, 1500);
  if (reply.t !== "dat" || !Array.isArray(reply.cols) || !Array.isArray(reply.dat) || reply.cols.length !== reply.dat.length) {
    throw new Error(`the air conditioner sent an unexpected status reply (${reply.t})`);
  }
  const raw: Record<string, number> = {};
  reply.cols.forEach((col: string, i: number) => {
    // Some columns are strings on some firmware, and only numeric ones are asked for.
    if (typeof reply.dat[i] === "number") raw[col] = reply.dat[i];
  });
  return raw;
}

/** Whichever of these addresses has a unit. Sends only the scan, which changes nothing. */
function scanUnits(ctx: Ctx, hosts: string[]): { host: string; mac: string }[] {
  const units: { host: string; mac: string }[] = [];
  for (const reply of ctx.udp.scan(hosts, PORT, ctx.crypto.hex(JSON.stringify({ t: "scan" })), { timeoutMs: 1500 })) {
    try {
      const envelope = JSON.parse(ctx.crypto.text(reply.data));
      const found = unseal(ctx, envelope.tag ? GCM_KEY : ECB_KEY, envelope);
      if (typeof found.mac === "string" && found.mac !== "") units.push({ host: reply.from, mac: found.mac });
    } catch {
      // Not a Gree unit.
    }
  }
  return units;
}

/** Tries the other addresses of the unit's /24 and moves to the one with its MAC. Sends only the scan, which changes nothing. */
function rediscover(ctx: Ctx): boolean {
  const now = Date.now();
  if (failingSince === 0) failingSince = now;
  if (now - failingSince < SWEEP_AFTER_MS || now - lastSweep < SWEEP_EVERY_MS) return false;
  const current = address || ctx.settings.host;
  const match = /^(\d+\.\d+\.\d+)\.(\d+)$/.exec(current);
  if (!match || !/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(current)) return false;
  lastSweep = now;
  const hosts: string[] = [];
  for (let i = 1; i < 255; i++) if (String(i) !== match[2]) hosts.push(`${match[1]}.${i}`);

  const units = scanUnits(ctx, hosts);
  const want = (ctx.settings.mac || knownMac).toLowerCase();
  // Without a MAC, one unit answering is taken to be this one. Several can't be told apart, so none is chosen.
  const unit = want ? units.find((u) => u.mac.toLowerCase() === want) : units.length === 1 ? units[0] : undefined;
  if (!unit) {
    if (!want && units.length > 1) ctx.log(`${units.length} units answered and the MAC isn't known. Set the MAC address to find this one.`);
    return false;
  }
  ctx.log(`the air conditioner moved from ${current} to ${unit.host}`);
  address = unit.host;
  session = null;
  return true;
}

function read(ctx: Ctx): { s: Session; raw: Record<string, number> } {
  if (session !== null) {
    try {
      return { s: session, raw: status(ctx, session) };
    } catch {
      // The unit may have restarted and forgotten the key, or moved: bind again once.
    }
  }
  session = connect(ctx);
  return { s: session, raw: status(ctx, session) };
}

function nameOf(raw: Record<string, number>, col: string, names: string[]): string | null {
  return col in raw ? names[raw[col]] ?? String(raw[col]) : null;
}

function boolOf(raw: Record<string, number>, col: string): boolean | null {
  return col in raw ? raw[col] !== 0 : null;
}

defineExtension({
  id: "gree",
  name: "Gree",
  version: "1.3.0",
  license: "MIT",
  description: "Gree-based air conditioners (Gree, Sinclair, EWPE Smart) over the local network.",
  categories: ["climate"],
  tags: ["Gree", "Sinclair", "EWPE Smart", "air conditioner", "AC", "heat pump"],
  access: { network: ["{host}"], lan: true, sockets: true },
  settings,
  devices: {
    everySeconds: 10,
    poll(ctx) {
      if (knownMac !== "" && ctx.removedDevices().includes(knownMac)) return [];
      let reading: { s: Session; raw: Record<string, number> };
      try {
        reading = read(ctx);
      } catch (e) {
        session = null;
        if (!rediscover(ctx)) throw e;
        reading = read(ctx);
      }
      failingSince = 0;
      const { s, raw } = reading;
      // Units report 0 when they have no reading, and most add 40 to the Celsius value.
      const sensor = raw.TemSen === undefined || raw.TemSen === 0 ? null : raw.TemSen >= 40 ? raw.TemSen - 40 : raw.TemSen;
      const values: DeviceValue[] = [
        { code: "power", type: "Boolean", writable: true, value: boolOf(raw, "Pow") },
        { code: "mode", type: "Enum", writable: true, value: nameOf(raw, "Mod", MODES), range: MODES },
        { code: "temp_set", type: "Integer", writable: true, value: raw.SetTem ?? null, min: 16, max: 30, step: 1, unit: "°C" },
        { code: "fan_speed", type: "Enum", writable: true, value: nameOf(raw, "WdSpd", FAN_SPEEDS), range: FAN_SPEEDS },
        { code: "quiet", type: "Boolean", writable: true, value: boolOf(raw, "Quiet") },
        { code: "turbo", type: "Boolean", writable: true, value: boolOf(raw, "Tur") },
        { code: "light", type: "Boolean", writable: true, value: boolOf(raw, "Lig") },
        { code: "temp_current", type: "Integer", value: sensor, unit: "°C" },
      ];
      // Only what this unit reports: units without a feature don't answer for its column.
      if ("SwUpDn" in raw) values.push({ code: "swing_vertical", type: "Enum", writable: true, value: nameOf(raw, "SwUpDn", SWING_VERTICAL), range: SWING_VERTICAL });
      if ("SwingLfRig" in raw) values.push({ code: "swing_horizontal", type: "Enum", writable: true, value: nameOf(raw, "SwingLfRig", SWING_HORIZONTAL), range: SWING_HORIZONTAL });
      if ("Buzzer_ON_OFF" in raw) values.push({ code: "beep", type: "Boolean", writable: true, value: raw.Buzzer_ON_OFF === 0 });
      for (const { col, code } of SWITCHES) {
        if (col in raw) values.push({ code, type: "Boolean", writable: true, value: boolOf(raw, col) });
      }
      return [{ id: s.mac, name: ctx.settings.name, kind: "climate", product: "Gree-based AC", values }];
    },
    discover(ctx) {
      const current = address || ctx.settings.host;
      const match = /^(\d+\.\d+\.\d+)\.\d+$/.exec(current);
      if (!match || !/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(current)) throw new Error("its address isn't on a private network, so there's nothing to look through");
      const hosts: string[] = [];
      for (let i = 1; i < 255; i++) hosts.push(`${match[1]}.${i}`);
      const own = (ctx.settings.mac || knownMac).toLowerCase();
      return scanUnits(ctx, hosts).map((u): FoundDevice => (
        // shortcut: one unit per integration, so another is only reported; let the settings hold a list when someone has two.
        u.mac.toLowerCase() === own || (own === "" && u.host === current)
          ? { id: u.mac, name: ctx.settings.name, product: "Gree-based AC", address: u.host }
          : { id: u.mac, name: "Another Gree air conditioner", product: "Gree-based AC", address: u.host, note: "This integration runs one air conditioner, the one at its address setting." }
      ));
    },
    write(ctx, deviceId, code, value) {
      const field = FIELDS.find((f) => f.code === code);
      if (!field) throw new Error(`the air conditioner has no value called ${code}`);
      if (session === null) session = connect(ctx);
      if (deviceId !== session.mac) throw new Error(`unknown air conditioner ${deviceId}`);

      const n = typeof value === "boolean" ? (value ? 1 : 0) : typeof value === "string" ? (NAMES[code] ?? []).indexOf(value) : Math.round(value);
      if (n < 0) throw new Error(`${value} isn't a ${code} this air conditioner has`);
      // Force Celsius, so a unit left in Fahrenheit reads SetTem correctly.
      const opt = code === "temp_set" ? ["TemUn", field.col] : [field.col];
      const p = code === "temp_set" ? [0, n] : [n];
      // Firmware 1.44 and later takes BuzzerCtrl (1 beeps, tested on a WB05RT13 module at 1.45); older units take Buzzer_ON_OFF, the other way round,
      // and report only that. A unit ignores the one it doesn't know.
      const cmd = code === "beep" ? { opt: ["BuzzerCtrl", field.col], p: [n, 1 - n] } : { opt, p };
      try {
        // One attempt: a lost command shows as an error, and the next poll binds again if the key went stale.
        const reply = exchange(ctx, session, { t: "cmd", ...cmd }, 0, 1, 3000);
        if (reply.t !== "res" || reply.r !== 200) throw new Error(`the air conditioner rejected the command (${reply.t}, ${reply.r})`);
      } catch (e) {
        session = null;
        throw e;
      }
    },
  },
});
