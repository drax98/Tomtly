// Gets a model's spec from miot-spec.org once, then keeps what's needed in ctx.storage, so a restart with no internet still
// has it. The address comes from the devices file's spec_type. The site's list of all models isn't used: it was 6.9 MB on
// 2026-11-01, over the 4 MiB a fetch may return.
import { compactSpec, SpecService } from "./spec";

export const SPEC_URL = "https://miot-spec.org/miot-spec-v2/instance?type=";
const SPEC_STORE_LIMIT = 900 * 1024; // the service allows 1 MiB, and refuses what goes over
const STORE_PREFIX = "spec1:"; // a new prefix once what compactSpec keeps changes
const INDEX_KEY = "spec1-index";
const FETCHES_PER_POLL = 3; // each can take 15 seconds
const FIRST_RETRY_MS = 60_000;
const LAST_RETRY_MS = 60 * 60_000;
// The server said it has no such model, which rarely changes, so it isn't asked again soon.
const MISSING_RETRY_MS = 6 * 60 * 60_000;

// As in the official list, sometimes with an extra part such as :0000C802.
const URN = /^urn:[a-z0-9-]+:device:[a-z0-9._-]+:[0-9a-f]{8}:[a-z0-9._-]+:\d+(:[0-9a-f]{8})?$/i;

type SpecContext = Pick<ExtensionContext<any>, "fetch" | "storage" | "log" | "crypto">;

const known = new Map<string, SpecService[]>();
const retries = new Map<string, { next: number; waitMs: number; missing: boolean }>();
let pausedUntil = 0; // after a failed request, so no internet costs one request a minute and not one per model

export function validUrn(value: unknown): value is string {
  return typeof value === "string" && value.length <= 120 && URN.test(value);
}

function bytes(ctx: SpecContext, text: string): number {
  return ctx.crypto.hex(text).length / 2;
}

function readIndex(ctx: SpecContext): [string, number][] {
  const index = ctx.storage.get(INDEX_KEY);
  return Array.isArray(index) ? index : [];
}

/** Saves it if it fits, dropping the oldest to make room. A spec that doesn't get saved is only fetched again after a restart. */
function save(ctx: SpecContext, urn: string, services: SpecService[]): void {
  const text = JSON.stringify(services);
  const size = bytes(ctx, text) + STORE_PREFIX.length + urn.length;
  if (size > SPEC_STORE_LIMIT / 2) return;
  try {
    const index = readIndex(ctx);
    while (index.reduce((sum, entry) => sum + entry[1], 0) + size > SPEC_STORE_LIMIT - 64 * 1024) { // the index itself takes room too
      const oldest = index.shift();
      if (!oldest) break;
      ctx.storage.delete(STORE_PREFIX + oldest[0]);
    }
    ctx.storage.set(STORE_PREFIX + urn, services);
    index.push([urn, size]);
    ctx.storage.set(INDEX_KEY, index);
  } catch (error) {
    ctx.log(`couldn't save a device description: ${error}`);
  }
}

/** The spec if it's already here, without any request. */
export function cachedSpec(ctx: SpecContext, urn: string): SpecService[] | undefined {
  const inMemory = known.get(urn);
  if (inMemory) return inMemory;
  const saved = ctx.storage.get(STORE_PREFIX + urn);
  if (!Array.isArray(saved)) return undefined;
  known.set(urn, saved);
  return saved;
}

function failed(urn: string, now: number, missing: boolean): void {
  const before = retries.get(urn);
  const waitMs = missing ? MISSING_RETRY_MS : Math.min(before ? before.waitMs * 2 : FIRST_RETRY_MS, LAST_RETRY_MS);
  retries.set(urn, { next: now + waitMs, waitMs, missing });
}

/** Whether the spec server said it has no such model, rather than not answering. */
export function specMissing(urn: string): boolean {
  return retries.get(urn)?.missing === true;
}

/** Asks for specs nobody has yet, a few at a time, and returns how many requests that took. Never throws. */
export function fetchSpecs(ctx: SpecContext, urns: string[], now: number, stopAt: number): number {
  let requests = 0;
  if (now < pausedUntil) return 0;
  for (const urn of urns) {
    const retry = retries.get(urn);
    if ((retry && now < retry.next) || known.has(urn)) continue;
    if (requests >= FETCHES_PER_POLL || Date.now() > stopAt) break;
    requests++;
    let missing = false;
    try {
      const response = ctx.fetch(SPEC_URL + urn);
      missing = response.status === 404;
      if (!response.ok) throw new Error(`status ${response.status}`);
      const services = compactSpec(response.json());
      if (services === null) {
        missing = true;
        throw new Error("that isn't a spec");
      }
      known.set(urn, services);
      retries.delete(urn);
      save(ctx, urn, services);
    } catch (error) {
      failed(urn, now, missing);
      ctx.log(`no description yet for a device: ${error}`);
      if (!missing) {
        pausedUntil = now + FIRST_RETRY_MS;
        break;
      }
    }
  }
  return requests;
}
