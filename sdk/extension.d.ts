// The API for building an extension. Copy this file next to your extension.ts to get autocomplete and type checks.
// An extension is plain synchronous TypeScript: there is no async, setTimeout or file access. All it can reach is on ctx.
// See docs/extensions.md.

/** What someone running your extension sets under "settings" in config.json. */
interface SettingDefinition {
  /** Shown on the page, and in the error when a required setting is missing. Say what it is. */
  label: string;
  /**
   * "secret" is a string that's redacted from logs and errors, and refused if your code returns it.
   * "file" is a file the owner chooses on the page, kept encrypted, and your code receives its text (1 MiB at most). Its whole text
   * and any long stretch of it are refused in any request, socket payload or output. It can't have a default.
   * See "What the secret checks catch" in docs/extension-api.md.
   */
  type: "string" | "number" | "boolean" | "secret" | "file";
  required?: boolean;
  /** Not allowed on a secret, a file, or a setting named by {...} in access.network or sendTo. */
  default?: string | number | boolean;
  /**
   * Secrets only: the hosts this value may be sent to, each also in access.network ("{host}" counts as written).
   * The service refuses, and records as a violation, any request or socket payload that holds the value and goes
   * anywhere else, whether it's written as is, URL-encoded, base64, hex or escaped as JSON. A value you transform first
   * (a hash, say) can't be recognised. Without sendTo it may go nowhere, which suits a key used only to sign. The owner
   * approves each destination. A broadcast never may have it.
   */
  sendTo?: readonly string[];
  /**
   * Files only, for a JSON file: fields whose values are secrets of their own, such as ["key"], at any depth. List every
   * one: nothing else in the file is recognised on its own, so a field left out can be published or sent.
   */
  secretFields?: readonly string[];
  /** Where to get the value, shown on the page. */
  help?: string;
}

type SettingsDefinition = Record<string, SettingDefinition>;

type SettingValue<T extends SettingDefinition> = T["type"] extends "number" ? number : T["type"] extends "boolean" ? boolean : string;

/** Your settings by key. A setting that's neither required nor has a default may be undefined. */
type SettingValues<S extends SettingsDefinition> = {
  [K in keyof S]: S[K] extends { required: true } | { default: string | number | boolean } ? SettingValue<S[K]> : SettingValue<S[K]> | undefined;
};

interface FetchOptions {
  /** GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS. Anything else is refused. */
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  /** A binary body as hex, instead of body. */
  bodyHex?: string;
}

/** How long to wait, 3 seconds by default and at most 15. */
interface SocketOptions {
  timeoutMs?: number;
}

/** For ctx.tcp.connect. */
interface TcpOptions extends SocketOptions {
  /** Speak TLS on this connection. */
  tls?: boolean;
  /** The name to verify the certificate against, the host by default. Another name must be a host name, and is checked like anything else you send. */
  serverName?: string;
  /** Skip certificate checks, for a self-signed device. Refused for a host that isn't on the local network. */
  insecure?: boolean;
}

/** A UDP port you listen on. Keep it in a top-level variable and read from it in background.run. */
interface UdpListener {
  /** Returns the next datagram, or null if none arrived in time. Waits 3 seconds unless you say, and never more than 2. */
  read(options?: SocketOptions): { from: string; data: string } | null;
  close(): void;
}

/** UDP, with binary data as hex strings. Needs access.sockets, and hosts must be in access.network. */
interface ExtensionUdp {
  /**
   * Receives datagrams on a port listed in access.listen, from anyone who can reach it. Needs no
   * access.sockets. From Docker the port must also be published, such as "5683:5683/udp" in compose.yaml.
   * Counts toward the 16 open connections, and closes when the extension stops.
   */
  listen(port: number): UdpListener;
  /** Sends one datagram and returns the first reply. Throws if none arrives in time. */
  request(host: string, port: number, data: string, options?: SocketOptions): string;
  /**
   * Sends one datagram to each address at once, and returns the first reply from each, within 2 seconds at most. For
   * finding a device that moved, on networks where a broadcast doesn't get through. Needs access.sockets and access.lan.
   * Up to 254 private IPv4 addresses. Every 8 addresses count as one operation.
   */
  scan(hosts: string[], port: number, data: string, options?: SocketOptions): { from: string; data: string }[];
  /** Broadcasts on the local network and returns every reply in time. Needs access.lan as well. A payload holding a secret is refused. */
  discover(port: number, data: string, options?: SocketOptions): { from: string; data: string }[];
}

/** A TCP connection that can stay open between calls, for protocols with sessions. Binary data is hex strings. */
interface TcpConnection {
  write(data: string): void;
  /** Returns what has arrived, or "" if nothing did in time. Throws if the connection closed. */
  read(options?: SocketOptions): string;
  close(): void;
}

/** A TCP port you listen on. Keep it in a top-level variable and call accept in background.run. */
interface TcpListener {
  /** Returns the next connection, or null if none arrived in time. Waits 3 seconds unless you say, and never more than 2. */
  accept(options?: SocketOptions): TcpConnection | null;
  close(): void;
}

interface ExtensionTcp {
  /**
   * Accepts connections on a port listed in access.listen, from anyone who can reach it. Needs no
   * access.sockets. From Docker the port must also be published. A listener and each connection it accepts count
   * toward the 16 open, and all close when the extension stops.
   */
  listen(port: number): TcpListener;
  /**
   * Returns which addresses accept a connection on the port, trying them all at once, within 2 seconds at most. Each
   * connection closes at once and nothing is sent. Needs access.sockets and access.lan. Up to 254 private IPv4 addresses.
   */
  scan(hosts: string[], port: number, options?: SocketOptions): string[];
  /** Needs access.sockets. Opens a connection to a host in access.network. Up to 16 can be open, and all close when the extension stops. */
  connect(host: string, port: number, options?: TcpOptions): TcpConnection;
}

interface AesOptions {
  mode: "ecb" | "cbc" | "gcm";
  /** Hex: 16, 24 or 32 bytes. */
  key: string;
  /** Hex. Required for cbc (16 bytes) and gcm. */
  iv?: string;
  /** Hex. Additional authenticated data for gcm. */
  aad?: string;
  /** Hex. */
  data: string;
  /** PKCS#7 padding for ecb and cbc. */
  padding?: boolean;
}

/** What device protocols tend to need. Everything binary is a hex string. */
interface ExtensionCrypto {
  hash(algorithm: "md5" | "sha1" | "sha256" | "sha512", data: string): string;
  hmac(algorithm: "md5" | "sha1" | "sha256" | "sha512", key: string, data: string): string;
  encrypt(options: AesOptions): string;
  decrypt(options: AesOptions): string;
  /** Secure random bytes, 1 to 4096. */
  random(size: number): string;
  /** Text to hex, as UTF-8. */
  hex(text: string): string;
  /** Hex to text. */
  text(data: string): string;
  fromBase64(text: string): string;
  toBase64(data: string): string;
}

/** Values that survive a restart, like a rotating refresh token. Needs access.storage. Up to 1 MiB in total, and deleted with the extension. */
interface ExtensionStorage {
  get(key: string): any;
  set(key: string, value: unknown): void;
  delete(key: string): void;
}

interface FetchResponse {
  status: number;
  /** True for a 2xx status. Redirects aren't followed, so check for 3xx yourself if a service uses them. */
  ok: boolean;
  text: string;
  /** The body as hex, for binary responses. */
  hex(): string;
  json(): any;
  header(name: string): string;
}

/** All your extension can do outside its own code. It's passed to each of your functions. */
interface ExtensionContext<S extends SettingsDefinition> {
  settings: SettingValues<S>;
  /**
   * Requests a host from access.network, and waits for the answer. Throws if it can't be reached, within 15 seconds.
   * Internet hosts must use https. Responses over 4 MiB are refused.
   */
  fetch(url: string, options?: FetchOptions): FetchResponse;
  udp: ExtensionUdp;
  tcp: ExtensionTcp;
  crypto: ExtensionCrypto;
  storage: ExtensionStorage;
  /**
   * Sets the plugin's data now, for a webhook or a background loop that has something new. The same rules as what
   * data.fetch returns: anything JSON can hold, up to 1 MiB, and never a secret. Needs data, webhooks or background.
   */
  publish(data: unknown): void;
  /** Runs devices.poll straight after the current call, so devices show what just changed. Needs devices. */
  refreshDevices(): void;
  /**
   * The ids of the devices the owner removed. The service hides them whatever you do, so this only saves work: don't
   * spend reads or connections on them. They can come back at any time. Needs devices.
   */
  removedDevices(): string[];
  /** Writes to the service's log, with secrets removed. */
  log(message: string): void;
}

/** A message sent to your webhook address. */
interface WebhookRequest {
  method: "GET" | "POST" | "PUT";
  /** What follows the address, starting with "/". "/" if nothing does. */
  path: string;
  /** The first value of each query parameter. */
  query: Record<string, string>;
  /** Lower-case names. There is never a cookie. */
  headers: Record<string, string>;
  /** Up to 1 MiB, read as text. */
  body: string;
  /** The same bytes as hex, for signatures and binary bodies. */
  bodyHex: string;
}

/** What to answer. Return nothing for a 204. */
interface WebhookResponse {
  /** 200 to 599, 200 by default. */
  status?: number;
  /** Text as it is, or anything JSON can hold, which is sent as JSON. */
  body?: unknown;
  /** Only Content-Type, Cache-Control, ETag, Retry-After and names starting X- are sent. A secret in the answer is replaced with ***. */
  headers?: Record<string, string>;
}

/** One value on a device. See docs/extension-api.md for what the page does with these. */
interface DeviceValue {
  /** The name scripts and the page use, unique within the device. */
  code: string;
  /** Boolean, Enum, Integer and Action can be writable. String and Number are read-only. */
  type: "Boolean" | "Enum" | "Integer" | "Action" | "String" | "Number";
  writable?: boolean;
  /** Null while unknown. Never invent a value: scripts treat unknown differently from off. */
  value: boolean | number | string | null;
  /** The choices for an Enum or Action. */
  range?: string[];
  min?: number;
  max?: number;
  step?: number;
  unit?: string;
  /** How to display an Integer: 205 with scale 1 reads as 20.5. */
  scale?: number;
  /** A setting rarely changed, such as a calibration: the device screen folds it away under the everyday values. */
  advanced?: boolean;
}

interface ExtensionDevice {
  /** Stable across restarts and unique within your extension. Scripts see it as "<your id>:<this id>". */
  id: string;
  name: string;
  /** climate, sensor, light, plug, cover, vacuum, air, fan, safety, infrastructure, or your own. */
  kind?: string;
  product?: string;
  /** True by default. Set false when the device didn't answer, while the service itself did. */
  online?: boolean;
  values: DeviceValue[];
}

/** A device devices.discover found. */
interface FoundDevice {
  /** The id devices.poll uses for it, if it has one. Otherwise anything stable. */
  id: string;
  name?: string;
  product?: string;
  /** Where it answered. Shown only to administrators. */
  address?: string;
  /** For one this extension can't use yet: what the owner has to do, like fetching a devices file again. */
  note?: string;
}

type WidgetTone = "good" | "warn" | "bad";
type WidgetValue = boolean | number | string | null;

/**
 * One piece of a widget on Home. A widget is data: the page draws it in its own style. Inputs (number, toggle, select)
 * hold what someone types or picks, and a button sends all of them, with its args, to the action of yours it names.
 */
type WidgetBlock =
  /** Starts a group titled label: the blocks after it, up to the next group, are drawn together under that title. */
  | { type: "group"; label: string }
  | { type: "text"; text: string; tone?: WidgetTone }
  | { type: "stat"; label: string; value?: WidgetValue; unit?: string; tone?: WidgetTone }
  | { type: "rows"; rows: { label: string; value?: WidgetValue; tone?: WidgetTone }[] }
  /** A device's value with its control, like on the Devices page, by its full id ("<your id>:<device id>") and code. min
   * and max narrow a slider to the range that matters, in the units shown, never past the device's own limits. */
  | { type: "device"; device: string; code: string; label?: string; min?: number; max?: number }
  | { type: "number"; name: string; label?: string; value?: number; min?: number; max?: number; step?: number; unit?: string }
  | { type: "toggle"; name: string; label?: string; value?: boolean }
  | { type: "select"; name: string; label?: string; value?: string; options: string[] }
  /** A time of day as "HH:MM", in steps of step minutes (1 by default). */
  | { type: "time"; name: string; label?: string; value?: string; step?: number }
  | { type: "button"; label: string; action: string; args?: Record<string, unknown>; tone?: WidgetTone }
  /**
   * A segmented control of 2 to 4 options. Picking one calls action at once with { [name]: option, ...args }, and
   * nothing else: not the widget's inputs, nor a dialog's form, which stays open. The pick shows busy until render
   * gives it as value, for up to 10 seconds.
   */
  | { type: "choice"; name: string; label: string; value?: string; options: string[]; action: string; args?: Record<string, unknown> }
  /**
   * A day's program, only in a dialog: up to 12 periods, each a start "HH:MM" and a whole temperature from min to max,
   * either left out while unknown. now is the index of the period running now. Save sends [{ time, temp }, ...] for
   * every period, or null when all are empty. Adjacent schedules show as tabs by label, opening on the one with now.
   * Periods out of order are shown and must be fixed before Save, but only once someone edits that schedule.
   */
  | { type: "schedule"; name: string; label: string; periods: { time?: string; temp?: number }[]; now?: number; min?: number; max?: number; step?: number; unit?: string }
  /**
   * A button labelled label that opens a form: title heads it, text explains it, and blocks are its fields (group, text,
   * rows, number, toggle, select, time, schedule and choice, up to 40). Save calls action with the form's values by
   * name, plus args, the way a button does; Cancel sends nothing. A form without inputs has only Close. Each opening
   * starts from the values render gives, and edits outlast a redraw while it's open.
   */
  | { type: "dialog"; label: string; action: string; title?: string; text?: string; args?: Record<string, unknown>; blocks: WidgetBlock[] };

type ExtensionLicense = "MIT" | "Apache-2.0" | "BSD-2-Clause" | "BSD-3-Clause" | "ISC" | "MPL-2.0" | "LGPL-2.1-only" | "LGPL-2.1-or-later"
  | "LGPL-3.0-only" | "LGPL-3.0-or-later" | "GPL-2.0-only" | "GPL-2.0-or-later" | "GPL-3.0-only" | "GPL-3.0-or-later" | "AGPL-3.0-only"
  | "AGPL-3.0-or-later" | "EUPL-1.2" | "0BSD" | "Unlicense";

type ExtensionCategory = "climate" | "lighting" | "cleaning" | "energy" | "security" | "sensors" | "media" | "weather" | "bridges" | "other";

interface ExtensionDefinition<S extends SettingsDefinition> {
  /** Lowercase letters, digits and -. Scripts use it, so changing it breaks them. */
  id: string;
  name: string;
  /** Semantic version, such as "1.2.0". */
  version: string;
  description?: string;
  homepage?: string;
  /**
   * The open license your code is under, as an SPDX id, with its text in a LICENSE file next to this one. The marketplace
   * lists only open source: MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, MPL-2.0, the LGPL, GPL and AGPL versions,
   * EUPL-1.2, 0BSD or Unlicense.
   */
  license?: ExtensionLicense;
  /** Where the marketplace files it. Pick from the fixed list; people filter by these. */
  categories?: ExtensionCategory[];
  /** Up to 20 words people may search for, of up to 40 characters each: the brands, models and devices it works with. */
  tags?: string[];
  /**
   * Everything this extension needs to function, shown on the page and approved by the person running it before any
   * of your code runs. Nothing is granted by default: the service refuses anything not listed here, records each
   * attempt, and stops an extension that keeps trying. ctx.crypto, ctx.log and ctx.settings need no access.
   * Asking for more in a new version means asking for approval again.
   */
  access?: {
    /**
     * Hosts for ctx.fetch and sockets. "api.example.com" is an internet host, https only.
     * "{host}" reads the host from your string setting called host, and may be on the local network. That setting can't
     * have a default, and the owner approves the address itself.
     */
    network?: string[];
    /** Any private address, for discovering devices with ctx.udp.discover. Ask only if you need it. */
    lan?: boolean;
    /** ctx.udp and ctx.tcp. */
    sockets?: boolean;
    /** ctx.storage. */
    storage?: boolean;
    /**
     * Gives you a private address on this service, "/hooks/<id>/<secret>", that anything on the internet which knows it
     * can send messages to, and webhooks.receive answers them. The owner sees the address once when approving.
     */
    webhooks?: boolean;
    /** Ports for ctx.udp.listen and ctx.tcp.listen. */
    listen?: number[];
  };
  /** Dynamic code is not available: eval and the Function constructor throw, and are recorded as violations. */
  settings?: S;
  /**
   * Makes this a plugin: data scripts read with home.plugin("<id>").data(). A plugin can also have only webhooks or
   * background and set its data with ctx.publish.
   */
  data?: {
    /** 300 by default. */
    everySeconds?: number;
    /** Returns the latest data, anything JSON can hold, up to 1 MiB. Throw on failure and the last good data stays. */
    fetch(ctx: ExtensionContext<S>): unknown;
  };
  /** Makes this an integration: devices on the page and to scripts through home.device("<id>:<device id>"). */
  devices?: {
    /** 30 by default. */
    everySeconds?: number;
    /** Returns every device. Throw on failure and they all show as offline. */
    poll(ctx: ExtensionContext<S>): ExtensionDevice[];
    /** Sends one command. It's already checked against the value's type and limits. Leave it out if nothing is writable. */
    write?(ctx: ExtensionContext<S>, deviceId: string, code: string, value: boolean | number | string): void;
    /**
     * Looks on the network for this integration's devices, when its page is opened, at most every 30 seconds. Only send
     * what changes nothing on a device. Throw if the look itself failed. Leave it out if it has no way to look.
     */
    discover?(ctx: ExtensionContext<S>): FoundDevice[];
  };
  /** Messages from outside, such as a service that calls you when something happens. Needs access.webhooks. Makes this a plugin. */
  webhooks?: {
    /** Runs on the extension's one thread with its other calls, within 10 seconds. Throw and the sender gets a 502. */
    receive(ctx: ExtensionContext<S>, request: WebhookRequest): WebhookResponse | void;
  };
  /**
   * Work that goes on between polls, like holding a UDP or TCP read for a device that pushes its state. run is called
   * again each time it returns, and shares the one thread with everything else, so keep each call short: a read waits
   * at most 2 seconds.
   */
  background?: {
    /** At least 0.05, 1 by default. A run that throws is retried at most once a second. */
    everySeconds?: number;
    run(ctx: ExtensionContext<S>): void;
  };
  /** Things scripts can ask for with home.plugin("<id>").action("<name>", args), like sending a notification. */
  actions?: Record<string, (ctx: ExtensionContext<S>, args: any) => void>;
  /**
   * Widgets people can place on Home, by a key: letters, digits, - and _. Up to 10. Each render is drawn on your thread
   * every 15 seconds and after an action, up to 40 blocks, and never a secret. A button names one of your actions, which
   * gets every input's value by name and the button's args.
   */
  widgets?: Record<string, { title: string; render(ctx: ExtensionContext<S>): WidgetBlock[] }>;
}

/** Declares your extension. Call it once, from extension.ts. */
declare function defineExtension<const S extends SettingsDefinition = {}>(definition: ExtensionDefinition<S>): void;
