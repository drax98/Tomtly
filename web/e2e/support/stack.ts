import { randomUUID } from "node:crypto";
import { existsSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect } from "@playwright/test";

const here = process.cwd(); // web/e2e, in the runner container
const controlDir = join(here, ".run", "control");
export const automationsDir = join(here, ".run", "automations");
export const uploads = join(here, "fixtures", "uploads");

/** Asks run.mjs, on the host, to do one of its fixed list of things. */
async function control(verb: string, args: object = {}): Promise<string> {
  const id = randomUUID();
  const request = join(controlDir, `${id}.req`);
  writeFileSync(`${request}.tmp`, JSON.stringify({ verb, args }));
  renameSync(`${request}.tmp`, request);
  const answer = join(controlDir, `${id}.res`);
  await expect.poll(() => existsSync(answer), { timeout: 120_000, intervals: [50, 100, 250] }).toBe(true);
  const response = JSON.parse(readFileSync(answer, "utf8"));
  rmSync(answer);
  if (!response.ok) throw new Error(response.error);
  return response.output ?? "";
}

/** A new home: an empty database, fixture scripts, and the fake device back to how it started. Resolves when it answers. */
export async function freshHome(options: { config?: string; market?: "on" | "off" | "wrong-key"; automations?: string } = {}) {
  await control("reset", options);
  await resetDevice();
  await serviceAnswers();
}

export async function serviceAnswers() {
  await expect
    .poll(async () => {
      try {
        return (await fetch("http://localhost:8099/api/auth/status")).status;
      } catch {
        return 0;
      }
    }, { timeout: 60_000, intervals: [100, 250, 500] })
    .toBe(200);
}

export async function restartService() {
  await control("restart", { service: "home-control" });
  await serviceAnswers();
}

export const stopService = (name: "home-control" | "marketplace") => control("stop", { service: name });
export async function startService(name: "home-control" | "marketplace") {
  await control("start", { service: name });
  if (name === "home-control") await serviceAnswers();
}

/** Whether this copy of the repository has the marketplace service. The public one doesn't, and runs every home without one. */
export const marketOn = process.env.E2E_MARKET !== "off";

export const marketReset = async () => { if (marketOn) await control("marketReset"); };
/** The marketplace's own CLI. */
export const marketAdmin = (...args: string[]) => control("marketAdmin", { args });

export const setupCode = () => control("setupCode");

/** Submits one version of a fixture and leaves it pending. */
export const submitListing = (repo: string, ref: string) => control("marketPublish", { repo, ref, submitOnly: true });
/** Packs one version of a fixture and takes it through the marketplace's real submit and approve. */
export const approveListing = async (repo: string, ref: string) => { if (marketOn) await control("marketPublish", { repo, ref }); };

/** The fake device, reached through the same route an extension uses. */
const DEVICE = "http://10.77.0.20:9000";

export interface DeviceState {
  devices: { id: string; online: boolean; values: { code: string; value: unknown }[] }[];
  commands: { id: string; code: string; value: unknown }[];
  requests: { method: string; url: string; body: string; header: Record<string, string[]> }[];
}

export async function deviceState(): Promise<DeviceState> {
  const state = (await (await fetch(`${DEVICE}/test/state`)).json()) as DeviceState;
  return { ...state, commands: state.commands ?? [], requests: state.requests ?? [] };
}
export const resetDevice = () => fetch(`${DEVICE}/test/reset`, { method: "POST" });
export const changeDevice = (change: { id?: string; online?: boolean; code?: string; value?: unknown; refuse?: boolean }) =>
  fetch(`${DEVICE}/test/set`, { method: "POST", body: JSON.stringify(change) });
export const deviceValue = async (id: string, code: string) =>
  (await deviceState()).devices.find((d) => d.id === id)?.values.find((v) => v.code === code)?.value;
