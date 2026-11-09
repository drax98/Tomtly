/// <reference path="../../sdk/extension.d.ts" />
// A mock of the host API an extension sees as ctx. Every call is recorded, and the device replies are whatever a test
// hands in. The crypto is real (node's), written to match internal/extension/host.go, so byte assertions mean something.
import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes } from "node:crypto";
import { vi } from "vitest";

/** The text a timed-out UDP read throws, as the host passes it on. Gree and Xiaomi look for "timeout". */
export const TIMEOUT = "read udp 192.168.1.20:54321: i/o timeout";

/** Loads an extension.ts fresh, so top-level state from an earlier test can't leak into this one. */
export async function loadExtension(load: () => Promise<unknown>): Promise<any> {
  vi.resetModules();
  let captured: any = null;
  (globalThis as any).defineExtension = (definition: unknown) => {
    captured = definition;
  };
  await load();
  return captured;
}

const bytes = (hex: string) => Buffer.from(hex, "hex");

function unpad(data: Buffer): Buffer {
  const n = data[data.length - 1];
  if (data.length === 0 || n === 0 || n > 16 || n > data.length) throw new Error("bad padding: wrong key, or not padded");
  return data.subarray(0, data.length - n);
}

function pad(data: Buffer): Buffer {
  const n = 16 - (data.length % 16);
  return Buffer.concat([data, Buffer.alloc(n, n)]);
}

export const crypto: ExtensionCrypto = {
  hash: (algorithm, data) => createHash(algorithm).update(bytes(data)).digest("hex"),
  hmac: (algorithm, key, data) => createHmac(algorithm, bytes(key)).update(bytes(data)).digest("hex"),
  encrypt(o) {
    const key = bytes(o.key);
    const name = `aes-${key.length * 8}`;
    if (o.mode === "gcm") {
      const c = createCipheriv(`${name}-gcm`, key, bytes(o.iv ?? ""), { authTagLength: 16 });
      if (o.aad) c.setAAD(bytes(o.aad));
      // The host returns ciphertext and tag as one string.
      return Buffer.concat([c.update(bytes(o.data)), c.final(), c.getAuthTag()]).toString("hex");
    }
    const data = o.padding ? pad(bytes(o.data)) : bytes(o.data);
    if (data.length % 16 !== 0) throw new Error("data isn't a whole number of blocks; set padding to true when encrypting");
    const c = o.mode === "ecb" ? createCipheriv(`${name}-ecb`, key, null) : createCipheriv(`${name}-cbc`, key, bytes(o.iv ?? ""));
    c.setAutoPadding(false);
    return Buffer.concat([c.update(data), c.final()]).toString("hex");
  },
  decrypt(o) {
    const key = bytes(o.key);
    const name = `aes-${key.length * 8}`;
    const data = bytes(o.data);
    if (o.mode === "gcm") {
      const d = createDecipheriv(`${name}-gcm`, key, bytes(o.iv ?? ""), { authTagLength: 16 });
      if (o.aad) d.setAAD(bytes(o.aad));
      d.setAuthTag(data.subarray(data.length - 16));
      try {
        return Buffer.concat([d.update(data.subarray(0, data.length - 16)), d.final()]).toString("hex");
      } catch {
        throw new Error("cipher: message authentication failed");
      }
    }
    const d = o.mode === "ecb" ? createDecipheriv(`${name}-ecb`, key, null) : createDecipheriv(`${name}-cbc`, key, bytes(o.iv ?? ""));
    d.setAutoPadding(false);
    const plain = Buffer.concat([d.update(data), d.final()]);
    return (o.padding ? unpad(plain) : plain).toString("hex");
  },
  random(size) {
    if (size < 1 || size > 4096) throw new Error("random takes 1 to 4096 bytes");
    return randomBytes(size).toString("hex");
  },
  hex: (text) => Buffer.from(text, "utf8").toString("hex"),
  text: (data) => bytes(data).toString("utf8"),
  fromBase64: (text) => Buffer.from(text, "base64").toString("hex"),
  toBase64: (data) => bytes(data).toString("base64"),
};

/** What a fake server hands back for one ctx.fetch: a status and a body, as text or as an object sent as JSON. */
export interface Reply {
  status?: number;
  body?: unknown;
  headers?: Record<string, string>;
}

export interface FetchCall {
  url: string;
  options: FetchOptions;
}

export interface UdpCall {
  host: string;
  port: number;
  data: string;
  options?: SocketOptions;
}

/** A fake TCP connection. Reads return the queued chunks, then "" (a quiet line) and close throws like the host's. */
export class FakeConnection implements TcpConnection {
  written: string[] = [];
  closed = false;
  closes = 0;
  readCalls: SocketOptions[] = [];
  private queue: (string | Error)[] = [];

  constructor(public host: string, public port: number, public options?: TcpOptions) {}

  /** Queues what the next read returns. An Error queued here is thrown instead. */
  receive(...chunks: (string | Error)[]): this {
    this.queue.push(...chunks);
    return this;
  }

  write(data: string): void {
    if (this.closed) throw new Error("use of closed network connection");
    this.written.push(data);
  }

  read(options?: SocketOptions): string {
    this.readCalls.push(options ?? {});
    if (this.closed) throw new Error("use of closed network connection");
    const next = this.queue.shift();
    if (next === undefined) return "";
    if (next instanceof Error) throw next;
    return next;
  }

  close(): void {
    this.closes++;
    this.closed = true;
  }
}

export interface HostOptions {
  settings?: Record<string, unknown>;
  /** Answers a ctx.fetch. Throw to simulate an unreachable host. */
  fetch?: (url: string, options: FetchOptions) => Reply;
  /** Answers a ctx.udp.request with hex, or throws TIMEOUT. */
  udpRequest?: (host: string, port: number, data: string, options?: SocketOptions) => string;
  /** Answers a ctx.udp.scan. */
  udpScan?: (hosts: string[], port: number, data: string, options?: SocketOptions) => { from: string; data: string }[];
  /** Makes a ctx.tcp.connect. Defaults to a connection nothing answers on. */
  tcpConnect?: (host: string, port: number, options?: TcpOptions) => FakeConnection;
  /** Answers a ctx.tcp.scan with the addresses that accept. */
  tcpScan?: (hosts: string[], port: number, options?: SocketOptions) => string[];
  removed?: string[];
}

export interface Host {
  ctx: ExtensionContext<any>;
  logs: string[];
  fetches: FetchCall[];
  udpRequests: UdpCall[];
  udpScans: UdpCall[];
  connections: FakeConnection[];
  tcpScans: { hosts: string[]; port: number; options?: SocketOptions }[];
  refreshes: number;
  published: unknown[];
  storage: Map<string, unknown>;
}

function response(reply: Reply): FetchResponse {
  const status = reply.status ?? 200;
  const text = typeof reply.body === "string" ? reply.body : reply.body === undefined ? "" : JSON.stringify(reply.body);
  return {
    status,
    ok: status >= 200 && status < 300,
    text,
    hex: () => Buffer.from(text, "utf8").toString("hex"),
    json: () => JSON.parse(text),
    header: (name: string) => reply.headers?.[name.toLowerCase()] ?? "",
  };
}

/** Builds the ctx an extension is handed. Anything not set up by the test throws, so an unexpected call fails loudly. */
export function makeHost(options: HostOptions = {}): Host {
  const host: Host = {
    logs: [],
    fetches: [],
    udpRequests: [],
    udpScans: [],
    connections: [],
    tcpScans: [],
    refreshes: 0,
    published: [],
    storage: new Map(),
    ctx: null as unknown as ExtensionContext<any>,
  };
  const storage: ExtensionStorage = {
    get: (key) => host.storage.get(key),
    set: (key, value) => void host.storage.set(key, JSON.parse(JSON.stringify(value))),
    delete: (key) => void host.storage.delete(key),
  };
  host.ctx = {
    settings: (options.settings ?? {}) as any,
    fetch(url, opts) {
      host.fetches.push({ url, options: opts ?? {} });
      if (!options.fetch) throw new Error(`unexpected fetch of ${url}`);
      return response(options.fetch(url, opts ?? {}));
    },
    udp: {
      listen() {
        throw new Error("udp.listen isn't used by this extension");
      },
      request(h, port, data, opts) {
        host.udpRequests.push({ host: h, port, data, options: opts });
        if (!options.udpRequest) throw new Error(TIMEOUT);
        return options.udpRequest(h, port, data, opts);
      },
      scan(hosts, port, data, opts) {
        host.udpScans.push({ host: hosts.join(","), port, data, options: opts });
        return options.udpScan ? options.udpScan(hosts, port, data, opts) : [];
      },
      discover() {
        throw new Error("udp.discover isn't used by this extension");
      },
    },
    tcp: {
      listen() {
        throw new Error("tcp.listen isn't used by this extension");
      },
      scan(hosts, port, opts) {
        host.tcpScans.push({ hosts, port, options: opts });
        return options.tcpScan ? options.tcpScan(hosts, port, opts) : [];
      },
      connect(h, port, opts) {
        if (!options.tcpConnect) throw new Error("connection refused");
        const conn = options.tcpConnect(h, port, opts);
        host.connections.push(conn);
        return conn;
      },
    },
    crypto,
    storage,
    publish(data) {
      host.published.push(data);
    },
    refreshDevices() {
      host.refreshes++;
    },
    removedDevices: () => options.removed ?? [],
    log(message) {
      host.logs.push(message);
    },
  };
  return host;
}

/** A device's view of a poll result, by value code. */
export function valuesByCode(device: ExtensionDevice): Record<string, DeviceValue> {
  return Object.fromEntries(device.values.map((v) => [v.code, v]));
}
