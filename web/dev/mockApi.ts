import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin } from "vite";

// Dev-only stand-in for the Go service (run with MOCK=1). It never ships: vite.config only loads it when serving.
const perms: Record<string, string[]> = {
  viewer: ["devices.read", "automations.read", "extensions.read"],
  operator: ["devices.read", "devices.write", "automations.read", "extensions.read"],
  editor: ["devices.read", "devices.write", "automations.read", "automations.edit", "automations.live", "extensions.read"],
  admin: ["devices.read", "devices.write", "automations.read", "automations.edit", "automations.live", "automations.approve", "extensions.read", "extensions.manage", "users.manage", "audit.read", "settings.manage"],
};
const dp = (code: string, type: string, value: unknown, extra: object = {}) => ({ dp: code, code, type, values: {}, scale: 0, writable: false, value, ...extra });
const devices = [
  { id: "t1", adapter: "x", name: "Living room heating", kind: "climate", online: true, dps: [dp("switch", "Boolean", true, { writable: true }), dp("temp_current", "Integer", 214, { scale: 1, values: { unit: "°C" } }), dp("temp_set", "Integer", 220, { scale: 1, writable: true, values: { min: 50, max: 300, step: 5, unit: "°C" } }), dp("heating_state", "Boolean", true)] },
  { id: "t2", adapter: "x", name: "Bedroom heating", kind: "climate", online: true, dps: [dp("switch", "Boolean", true, { writable: true }), dp("temp_current", "Integer", 189, { scale: 1, values: { unit: "°C" } }), dp("temp_set", "Integer", 180, { scale: 1, writable: true, values: { min: 50, max: 300, step: 5, unit: "°C" } }), dp("heating_state", "Boolean", false), dp("window_state", "Boolean", true)] },
  { id: "l1", adapter: "x", name: "Sofa lamp", kind: "light", online: true, dps: [dp("switch_led", "Boolean", true, { writable: true }), dp("bright_value", "Integer", 600, { writable: true, values: { min: 10, max: 1000 } }), dp("colour_hue", "Integer", 220, { writable: true, values: { min: 0, max: 360 } }), dp("colour_saturation", "Integer", 1000, { writable: true, values: { min: 0, max: 1000 } })] },
  { id: "l2", adapter: "x", name: "Ceiling light", kind: "light", online: true, dps: [dp("switch_led", "Boolean", false, { writable: true })] },
  { id: "p1", adapter: "x", name: "Coffee machine", kind: "plug", online: true, dps: [dp("switch_1", "Boolean", false, { writable: true }), dp("cur_power", "Integer", 0, { values: { unit: "W" } })] },
  { id: "c1", adapter: "x", name: "Bedroom blind", kind: "cover", online: true, dps: [dp("control", "Enum", "stop", { writable: true, values: { range: ["open", "stop", "close"] } }), dp("percent_control", "Integer", 40, { writable: true, values: { min: 0, max: 100 } }), dp("percent_state", "Integer", 40)] },
  { id: "v1", adapter: "x", name: "Robot vacuum", kind: "vacuum", online: true, dps: [dp("command", "Action", null, { writable: true, values: { range: ["start", "pause", "dock", "find"] } }), dp("state", "Enum", "charging"), dp("battery", "Integer", 82, { values: { unit: "%" } }), dp("fan_speed", "Enum", "strong", { writable: true, values: { range: ["silent", "strong", "max"] } })] },
  { id: "s1", adapter: "x", name: "Balcony door", kind: "sensor", online: true, dps: [dp("open", "Boolean", true), dp("battery_percentage", "Integer", 15)] },
  { id: "s2", adapter: "x", name: "Hall window", kind: "sensor", online: false, dps: [dp("open", "Boolean", false)] },
];
const pins: Record<string, string[]> = {};
let dashboard: string[] = [];
let heatTarget = 21;
const mockWidgets = () => [
  { key: "script:heating/all", title: "All heaters", from: "heating", practising: true, blocks: [
    { type: "stat", label: "Living room", value: 21.4, unit: "°C" }, { type: "stat", label: "Bedroom", value: 18.9, unit: "°C", tone: "warn" },
    { type: "device", device: "t1", code: "switch" },
    { type: "number", name: "temp", label: "Target", value: heatTarget, min: 5, max: 30, step: 0.5, unit: "°C" }, { type: "button", label: "Set every heater", action: "apply" },
  ] },
  { key: "ext:weather/now", title: "Weather now", from: "Weather", blocks: [
    { type: "stat", label: "Outside", value: 7.3, unit: "°C" },
    { type: "rows", rows: [{ label: "Feels like", value: "4.1 °C" }, { label: "In 1 h", value: "7 °C, rain 70%", tone: "warn" }] },
  ] },
  { key: "ext:tides/next", title: "Next tide", from: "Tides", error: "render must return a list of blocks" },
];
let homeSettings = { language: "", timezone: "", latitude: null, longitude: null };
const rooms: Record<string, string[]> = { "Living room": ["t1", "l1", "l2", "p1", "v1", "s1"], Bedroom: ["t2", "c1"], Hall: ["s2"] };
const ext = (over: object) => ({ source: "github:a/b@v1", access: [], settings: [], provides: ["data"], running: true, removable: true, ...over });
const extensions = [
  ext({ id: "weather", name: "Weather", version: "1.2.0", description: "Local forecast for your scripts", risks: ["It holds your API key and can send data to api.open-meteo.com. Approve this only if you trust who wrote it."], access: ["network: api.open-meteo.com"], settings: [{ key: "city", label: "City", type: "string", required: true, set: true }] }),
  ext({ source: "upload:abc", id: "tides", name: "Tides", version: "0.3.0", needsApproval: true, running: false, access: ["network: tides.example.com", "network: *.cdn.example.com"], warnings: ["Can reach any subdomain of cdn.example.com"], violations: [{ time: new Date().toISOString(), extension: "tides", kind: "network", detail: "evil.example.net" }] }),
];
const market = (over: { id: string; [key: string]: unknown }) => ({ kind: "plugin", author: "Home Control", provides: ["data"], access: [], testedOn: "Simulator only", approvedOn: "2026-10-30", size: 4096, sha256: "3f2a91c7d4e08b16".repeat(4), installed: false, updateAvailable: false, repository: "https://github.com/home-control/extensions", ...over });
// A small valid PNG, so the page's images have something to show.
const mockImage = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");
const mockAsset = (name: string) => ({ name, type: "image/png", sha256: "c".repeat(64) });
const mockReviews: Record<string, { id: string; rating: number; text: string; name: string; at: string }[]> = {
  tides: [
    { id: "a1", rating: 5, text: "Spot on for our harbour.\nAnd <b>plain text</b> stays plain: https://example.com", name: "Ola", at: "2026-10-28T10:00:00Z" },
    { id: "a2", rating: 4, text: "", name: "", at: "2026-10-20T10:00:00Z" },
  ],
};
const summary = (id: string) => {
  const list = mockReviews[id] ?? [];
  return { average: list.length ? list.reduce((sum, r) => sum + r.rating, 0) / list.length : 0, count: list.length };
};
const marketEntries = [
  market({ id: "weather", name: "Weather", description: "Local forecast for your scripts", version: "1.3.0", access: ["Internet: api.open-meteo.com"], installed: true, installedVersion: "1.2.0", installedSource: "market:weather@1.2.0#" + "a".repeat(64), updateAvailable: true, upgradeSteps: [{ version: "1.3.0", note: "The forecast service now needs a personal access token.", ask: ["pat"] }] }),
  market({ id: "tides", name: "Tides", description: "Tide times for the harbour", version: "0.3.0", testedOn: "A real home for two weeks", access: ["Internet: tides.example.com"], icon: mockAsset("icon.png"), screenshots: [mockAsset("screenshot-1.png"), mockAsset("screenshot-2.png")] }),
  market({ id: "hue-bridge", name: "Hue bridge", kind: "integration", provides: ["devices"], description: "Lights from a Philips Hue bridge", version: "2.0.1", access: ["Any device on your local network", "Saved data"] }),
];
const pat = { key: "pat", label: "Personal access token", type: "secret", required: true, help: "Make one on the service's account page. (Type wrong to see a refusal.)", set: false };
const weatherUpdate = ext({ source: "market:weather@1.3.0#" + "b".repeat(64), id: "weather", name: "Weather", version: "1.3.0", running: false, access: ["Internet: api.open-meteo.com"], settings: [pat],
  upgrade: { from: "1.2.0", to: "1.3.0", steps: [{ version: "1.3.0", note: "The forecast service now needs a personal access token." }], ask: [pat, { key: "city", label: "City", type: "string", required: true, set: true, value: "Oslo" }] } });
const users = [
  { id: "u1", username: "ann", role: "admin", disabled: false, created: "2026-10-01T10:00:00Z" },
  { id: "u2", username: "ben", role: "operator", disabled: false, created: "2026-10-05T10:00:00Z" },
  { id: "u3", username: "guest", role: "viewer", disabled: true, created: "2026-10-20T10:00:00Z" },
];
const body = (req: IncomingMessage) => new Promise<string>((ok) => { let d = ""; req.on("data", (c) => (d += c)); req.on("end", () => ok(d)); });

export function mockApi(): Plugin {
  return {
    name: "home-control-mock-api",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/api", async (req: IncomingMessage, res: ServerResponse) => {
        const url = new URL(req.url ?? "/", "http://x");
        const path = url.pathname;
        const role = /mock_role=(\w+)/.exec(req.headers.cookie ?? "")?.[1];
        const send = (status: number, data: unknown) => { res.statusCode = status; res.setHeader("Content-Type", "application/json"); res.end(JSON.stringify(data)); };
        if (path === "/auth/status") return send(200, role === "setup" ? { setupRequired: true, user: null, permissions: [] } : role ? { setupRequired: false, user: { id: "u1", username: "ann", role }, permissions: perms[role] } : { setupRequired: false, user: null, permissions: [] });
        if (path === "/auth/login" || path === "/auth/setup") {
          const b = JSON.parse((await body(req)) || "{}");
          const r = b.username in perms ? b.username : "admin";
          res.setHeader("Set-Cookie", `mock_role=${r}; Path=/`);
          return send(200, { setupRequired: false, user: { id: "u1", username: b.username, role: r }, permissions: perms[r] });
        }
        if (!role) return send(401, { error: "sign in" });
        if (path === "/auth/logout") { res.setHeader("Set-Cookie", "mock_role=; Path=/; Max-Age=0"); return send(200, {}); }
        if (path === "/devices") return send(200, devices);
        if (path === "/rooms" && req.method === "POST") { const { name } = JSON.parse((await body(req)) || "{}"); if (rooms[name]) return send(409, { error: `there's already a room called ${name}` }); rooms[name] = []; return send(200, rooms); }
        const roomPath = /^\/rooms\/([^/]+)$/.exec(path);
        if (roomPath && req.method === "DELETE") { delete rooms[decodeURIComponent(roomPath[1])]; return send(200, rooms); }
        if (roomPath && req.method === "PATCH") {
          const from = decodeURIComponent(roomPath[1]), change = JSON.parse((await body(req)) || "{}");
          const entries = Object.entries(rooms);
          const i = entries.findIndex(([n]) => n === from);
          if (i < 0) return send(404, { error: "no such room" });
          const [moved] = entries.splice(i, 1);
          if (change.name) moved[0] = change.name;
          entries.splice(typeof change.position === "number" ? Math.max(0, Math.min(change.position, entries.length)) : i, 0, moved);
          for (const k of Object.keys(rooms)) delete rooms[k];
          for (const [n, ids] of entries) rooms[n] = ids;
          return send(200, rooms);
        }
        if (path === "/rooms") return send(200, rooms);
        const edit = /^\/devices\/([^/]+)$/.exec(path);
        if (edit && req.method === "PATCH") {
          const id = decodeURIComponent(edit[1]), change = JSON.parse((await body(req)) || "{}");
          const d = devices.find((x) => x.id === id) as { name: string; originalName?: string } | undefined;
          if (!d) return send(404, { error: "no such device" });
          if (typeof change.name === "string") { const original = d.originalName ?? d.name; d.name = change.name || original; d.originalName = change.name ? original : undefined; }
          if (typeof change.room === "string") {
            for (const ids of Object.values(rooms) as string[][]) { const i = ids.indexOf(id); if (i >= 0) ids.splice(i, 1); }
            if (change.room) ((rooms as Record<string, string[]>)[change.room] ??= []).push(id);
          }
          return send(200, {});
        }
        if (path === "/settings") {
          if (req.method === "PUT") homeSettings = JSON.parse((await body(req)) || "{}");
          return send(200, homeSettings);
        }
        if (path === "/widgets") return send(200, { layout: dashboard, widgets: mockWidgets() });
        if (path === "/dashboard" && req.method === "PUT") { dashboard = JSON.parse((await body(req)) || "{}").widgets ?? []; return send(200, { layout: dashboard }); }
        const press = /^\/widgets\/([^/]+)\/([^/]+)\/actions\/([^/]+)$/.exec(path);
        if (press) {
          const { input } = JSON.parse((await body(req)) || "{}");
          if (typeof input?.temp === "number") heatTarget = input.temp;
          return send(200, { live: decodeURIComponent(press[1]) !== "script:heating" });
        }
        if (path === "/pins") return send(200, pins);
        const pin = /^\/devices\/([^/]+)\/pins$/.exec(path);
        if (pin && req.method === "PUT") { pins[decodeURIComponent(pin[1])] = JSON.parse((await body(req)) || "{}").dps ?? []; return send(200, { dps: pins[decodeURIComponent(pin[1])] }); }
        if (path === "/automations" || path.endsWith("/access")) return send(200, { scripts: [{ name: "heating", live: true, access: [{ key: "device.write:boiler", label: "Change Boiler (boiler)", approved: true }, { key: "plugin.action:phone:notify", label: 'Run "notify" on the phone plugin', approved: false, refused: 12 }] }, { name: "night-lights", live: false, access: [{ key: "device.write:sofa", label: "Change Sofa lamp (sofa)", approved: false, refused: 3 }] }, { name: "away-mode", live: false, error: "away-mode.ts:12: Unexpected token" }], events: [{ time: new Date().toISOString(), script: "heating", kind: "set", message: "set Living room heating target to 22" }, { time: new Date().toISOString(), script: "night-lights", kind: "shadow", message: "would set Sofa lamp switch_led to false" }] });
        if (path === "/marketplace") return send(200, { configured: true, reachable: true, verified: true, site: "http://127.0.0.1:8101", entries: marketEntries.map((e) => ({ ...e, rating: summary(e.id) })) });
        const asset = /^\/marketplace\/[^/]+\/assets\/[^/]+$/.exec(path);
        if (asset) { res.setHeader("Content-Type", "image/png"); return res.end(mockImage); }
        const reviews = /^\/marketplace\/([^/]+)\/reviews$/.exec(path);
        if (reviews) {
          const id = reviews[1];
          if (req.method === "POST") {
            const b = JSON.parse((await body(req)) || "{}");
            // Type "offline" as a review to see a refusal.
            if (b.text === "offline") return send(502, { error: "the marketplace can't be reached right now" });
            mockReviews[id] = [{ id: "mine", rating: b.rating, text: b.text, name: b.name, at: new Date().toISOString() }, ...(mockReviews[id] ?? []).filter((r) => r.id !== "mine")];
            return send(200, { ok: true });
          }
          const offset = Number(url.searchParams.get("offset") ?? 0);
          return send(200, { reviews: (mockReviews[id] ?? []).slice(offset, offset + 20), ...summary(id) });
        }
        if (path === "/extensions/replace") {
          const b = JSON.parse((await body(req)) || "{}");
          return b.settings?.pat === "wrong" ? send(400, { error: 'the setting "pat" must be a token from the new service' }) : send(200, ext(extensions[0]));
        }
        if (path === "/marketplace/inspect" && JSON.parse((await body(req)) || "{}").id === "weather") return send(200, weatherUpdate);
        if (path === "/extensions/inspect" || path === "/extensions/upload") return send(200, weatherUpdate);
        if (path === "/marketplace/inspect") return send(200, ext({ source: "market:tides@0.3.0#" + "a".repeat(64), id: "tides", name: "Tides", version: "0.3.0", running: false, access: ["Internet: tides.example.com"] }));
        if (path === "/extensions/removed") return send(200, []);
        if (path === "/extensions") return send(200, extensions);
        if (path.endsWith("/discover")) return send(200, { supported: true, devices: [] });
        if (path === "/integrations") return send(200, [{ type: "tuya", name: "Tuya", description: "Smart plugs, lights and heating", devices: devices.map((d) => ({ id: d.id, name: d.name, kind: d.kind, online: d.online })), access: ["network: openapi.tuyaeu.com"] }, { type: "weather", name: "Weather", devices: [], extension: extensions[0] }]);
        if (path === "/users") return send(200, users);
        if (path === "/tokens") return send(200, [{ id: "t1", name: "Kitchen dashboard", role: "viewer", owner: "ann", created: "2026-10-10T10:00:00Z", lastUsed: null }]);
        if (path === "/audit") return send(200, Array.from({ length: 8 }, (_, i) => ({ time: new Date(Date.now() - i * 3600e3).toISOString(), user: i % 3 ? "ann" : "ben", action: i % 3 ? "device.write" : "login.success", detail: i % 3 ? "Sofa lamp switch_led" : "", ip: "192.168.1.20" })));
        if (path === "/automations/sdk") return send(200, {});
        if (path.endsWith("/source")) return send(200, { name: "heating", source: "// heating\nhome.reconcile(() => {});\n", version: "v1" });
        return send(200, {});
      });
    },
  };
}
