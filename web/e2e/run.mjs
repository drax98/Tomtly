#!/usr/bin/env node
// Builds the end-to-end stack, runs Playwright in Docker against it, and removes it all again. Exits with Playwright's code.
//
//	pnpm e2e                         everything
//	pnpm e2e specs/02-sign-in.spec.ts   any Playwright arguments
//	pnpm e2e --debug                 also publishes the page on 127.0.0.1:18099 and keeps the stack until Ctrl-C
//	pnpm e2e --no-build              reuses the images of the last run, for when only specs or fixtures changed
//
// Tests can't restart a container from inside one, and no container gets the Docker socket. Instead a test drops a request
// file in .run/control and this script, on the host, runs it. VERBS below is everything a test can ask for, and every
// Docker command this script runs names the project in compose.yaml, so nothing outside the e2e stack can be touched.
import { spawn, spawnSync } from "node:child_process";
import { chmodSync, cpSync, existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const run = join(here, ".run");
const controlDir = join(run, "control");
const automations = join(run, "automations");

const args = process.argv.slice(2);
const debug = args.includes("--debug");
const noBuild = args.includes("--no-build");
const playwrightArgs = args.filter((a) => a !== "--debug" && a !== "--no-build");

const version = JSON.parse(readFileSync(join(here, "..", "package.json"), "utf8")).devDependencies["@playwright/test"];
if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error(`@playwright/test must be an exact version, so the image matches it; it is ${version}`);
const installed = join(here, "..", "node_modules", "@playwright", "test", "package.json");
if (!existsSync(installed) || JSON.parse(readFileSync(installed, "utf8")).version !== version) throw new Error(`run pnpm install in web/ first: @playwright/test ${version} isn't there`);

// What compose.yaml reads. Nothing comes from the host's environment files: see --env-file below.
// The public copy of this repository has no marketplace service: then every home runs without one, and specs that need
// one skip themselves.
const withMarket = existsSync(join(here, "..", "..", "Dockerfile.marketplace"));
const vars = { E2E_PLAYWRIGHT_VERSION: version, E2E_CONFIG: "devices", E2E_MARKET: withMarket ? "on" : "off", E2E_MARKET_URL: "", E2E_MARKET_KEY: "", E2E_MARKET_SIGNING_KEY: "", E2E_MARKET_APPROVAL_KEY: "" };
let publicKey = "";
let otherPublicKey = "";
let approvalKey = ""; // goes to the command by standard input, as the marketplace wants: never in a container's environment

function compose(command, { capture = false, check = true, input, binary = false } = {}) {
  const files = ["-f", "compose.yaml", ...(debug ? ["-f", "compose.debug.yaml"] : [])];
  const result = spawnSync("docker", ["compose", "--project-directory", here, "--env-file", "/dev/null", ...files, "--profile", "runner", ...command], {
    cwd: here,
    env: { ...process.env, ...vars },
    stdio: [input === undefined ? "ignore" : "pipe", capture ? "pipe" : "ignore", "pipe"],
    input,
    maxBuffer: 16 << 20,
  });
  if (check && result.status !== 0) throw new Error(`docker compose ${command.join(" ")} failed:\n${result.stderr}`);
  return binary ? result : String(result.stdout ?? "").trim();
}

function freshAutomations(set) {
  rmSync(automations, { recursive: true, force: true });
  mkdirSync(automations, { recursive: true });
  cpSync(join(here, "fixtures", "home", "automations", set), automations, { recursive: true });
  chmodSync(automations, 0o777); // the service isn't root
}

const SERVICES = ["home-control", "marketplace"];
const service = (name) => {
  if (!SERVICES.includes(name)) throw new Error(`no such service: ${name}`);
  return name;
};
const marketKeys = { on: () => publicKey, off: () => "", "wrong-key": () => otherPublicKey };

const VERBS = {
  /** A new home: an empty database and scripts from a fixture folder, with one config and one state of the marketplace. */
  reset({ config = "devices", market = "on", automations: set = "default" }) {
    if (!/^[a-z-]+$/.test(config) || !/^[a-z-]+$/.test(set) || !marketKeys[market]) throw new Error("bad reset");
    compose(["rm", "-sf", "home-control"]);
    compose(["exec", "-T", "postgres", "psql", "-q", "-U", "home_control", "-d", "postgres", "-c", "DROP DATABASE IF EXISTS home_control WITH (FORCE)", "-c", "CREATE DATABASE home_control"]);
    freshAutomations(set);
    vars.E2E_CONFIG = config;
    if (!withMarket) market = "off";
    vars.E2E_MARKET_URL = market === "off" ? "" : "http://marketplace:8100";
    vars.E2E_MARKET_KEY = marketKeys[market]();
        compose(["up", "-d", "--no-deps", "home-control"]);
  },
  /** The one-time setup code, from where the service prints it. */
  setupCode() {
    return /setupCode=(\S+)/.exec(compose(["logs", "--no-color", "home-control"], { capture: true }))?.[1] ?? "";
  },
  restart({ service: name }) {
    compose(["restart", service(name)]);
    if (name === "marketplace") compose(["up", "-d", "--wait", "--no-deps", "marketplace"]);
  },
  stop({ service: name }) {
    compose(["stop", service(name)]);
  },
  start({ service: name }) {
    compose(["up", "-d", "--no-deps", ...(name === "marketplace" ? ["--wait"] : []), service(name)]);
  },
  /** An empty catalog. */
  marketReset() {
    compose(["rm", "-sf", "marketplace", "marketplace-db"]);
    compose(["up", "-d", "--wait", "marketplace"]);
  },
  /** The marketplace's real path for one version of a fixture: pack, submit, read it back, approve with the approval key. */
  marketPublish({ repo, ref, submitOnly = false }) {
    if (!/^[a-z-]+$/.test(repo) || !/^v[0-9.]+$/.test(ref)) throw new Error("bad listing");
    const folder = `/source/e2e/${repo}/${ref}`;
    const packed = compose(["exec", "-T", "marketplace", "/marketplace", "pack", folder], { capture: true, binary: true });
    const sha256 = /sha256 ([0-9a-f]{64})/.exec(String(packed.stderr))[1];
    const by = ["--by", "E2E Reviewer"];
    const listing = existsSync(join(here, "fixtures", "github", "e2e", repo, ref, "listing")) ? ["--listing", `${folder}/listing`] : [];
    compose(["exec", "-T", "marketplace", "/marketplace", "submit", "--file", "-", ...by, ...listing, "--repository", `https://github.com/e2e/${repo}`], { input: packed.stdout });
    compose(["exec", "-T", "marketplace", "/marketplace", "show", repo, "--version", ref.slice(1)], { capture: true });
    if (submitOnly) return;
    compose(["exec", "-T", "marketplace", "/marketplace", "approve", "--id", repo, "--version", ref.slice(1), "--sha256", sha256, ...by, "--tested-on", "Simulator only", "--key-file", "-"], { input: approvalKey });
    VERBS.restart({ service: "marketplace" });
  },
  /** The marketplace's own admin command, then a restart so the page sees it now and not within its 20 second poll. */
  marketAdmin({ args: admin }) {
    if (!["reject", "withdraw", "suspend", "unsuspend", "list", "pending", "audit", "reviews"].includes(admin[0])) throw new Error(`no such command: ${admin[0]}`);
    const output = compose(["exec", "-T", "marketplace", "/marketplace", ...admin], { capture: true });
    if (!["list", "pending", "audit", "reviews"].includes(admin[0])) VERBS.restart({ service: "marketplace" });
    return output;
  },
};

function answerControl() {
  for (const name of readdirSync(controlDir)) {
    if (!name.endsWith(".req")) continue;
    const path = join(controlDir, name);
    const request = JSON.parse(readFileSync(path, "utf8"));
    rmSync(path);
    let response;
    try {
      if (!Object.hasOwn(VERBS, request.verb)) throw new Error(`no such verb: ${request.verb}`);
      response = { ok: true, output: VERBS[request.verb](request.args ?? {}) };
    } catch (e) {
      response = { ok: false, error: String(e) };
    }
    const answer = path.replace(/\.req$/, ".res");
    writeFileSync(`${answer}.tmp`, JSON.stringify(response));
    renameSync(`${answer}.tmp`, answer);
  }
}

function cleanUp() {
  compose(["down", "-v", "--remove-orphans", "--timeout", "3"], { check: false });
  rmSync(run, { recursive: true, force: true });
}

function say(text) {
  process.stdout.write(`\n[e2e] ${text}\n`);
}

function runPlaywright() {
  return new Promise((resolve) => {
    const child = spawn("docker", ["compose", "--project-directory", here, "--env-file", "/dev/null", "-f", "compose.yaml", ...(debug ? ["-f", "compose.debug.yaml"] : []), "--profile", "runner",
      "run", "--rm", "-T", "playwright", "node", "/work/node_modules/@playwright/test/cli.js", "test", "-c", "playwright.config.ts", ...playwrightArgs], { cwd: here, env: { ...process.env, ...vars }, stdio: ["ignore", "inherit", "inherit"] });
    const poll = setInterval(answerControl, 100);
    child.on("exit", (code) => {
      clearInterval(poll);
      resolve(code ?? 1);
    });
  });
}

let code = 1;
let interrupted = false;
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    if (interrupted) return;
    interrupted = true;
    say("stopping and removing the stack");
    cleanUp();
    process.exit(130);
  });
}

try {
  cleanUp(); // whatever a killed run left
  rmSync(join(here, ".results"), { recursive: true, force: true });
  mkdirSync(controlDir, { recursive: true });
  chmodSync(controlDir, 0o777);
  freshAutomations("default");

  if (!noBuild) {
    say("building images one at a time");
    compose(["--parallel", "1", "build", "fake-device", "fake-github", ...(withMarket ? ["marketplace"] : []), "home-control"]);
  }

  // The marketplace's real signing setup, with a key that lives only as long as this run.
  if (withMarket) {
    vars.E2E_MARKET_SIGNING_KEY = compose(["run", "--rm", "--no-deps", "-T", "marketplace", "generate-key"], { capture: true });
    publicKey = compose(["run", "--rm", "--no-deps", "-T", "marketplace", "public-key"], { capture: true });
    const signing = vars.E2E_MARKET_SIGNING_KEY;
    vars.E2E_MARKET_SIGNING_KEY = compose(["run", "--rm", "--no-deps", "-T", "marketplace", "generate-key"], { capture: true });
    otherPublicKey = compose(["run", "--rm", "--no-deps", "-T", "marketplace", "public-key"], { capture: true });
    vars.E2E_MARKET_SIGNING_KEY = signing;
    approvalKey = compose(["run", "--rm", "--no-deps", "-T", "marketplace", "generate-key"], { capture: true });
    vars.E2E_MARKET_APPROVAL_KEY = compose(["run", "--rm", "--no-deps", "-T", "marketplace", "public-key", "--key-file", "-"], { capture: true, input: approvalKey });
  }

  say("starting the stack");
  compose(["up", "-d", "--wait", "netns", "router", "fake-device", "fake-github", "postgres", ...(withMarket ? ["marketplace"] : [])]);

  say("running Playwright");
  code = await runPlaywright();
  say(`Playwright exited with ${code}. Report and traces: web/e2e/.results/report (open with pnpm exec playwright show-report e2e/.results/report)`);
  if (debug) {
    say("--debug: the page is on http://127.0.0.1:18099 until you press Ctrl-C");
    await new Promise(() => {});
  }
} catch (e) {
  console.error(e);
  code = 1;
} finally {
  if (!interrupted) {
    say("removing containers, volumes and networks");
    cleanUp();
  }
}
process.exit(code);
