export type Value = boolean | number | string;

export interface DataPoint {
  dp: string;
  code: string;
  type: string;
  values: { range?: string[]; min?: number; max?: number; step?: number; unit?: string };
  scale: number;
  writable: boolean;
  value: Value | null;
  /** A setting rarely changed, folded away under the everyday values. */
  advanced?: boolean;
}

export interface Device {
  id: string;
  adapter: string;
  name: string;
  kind: string;
  category: string;
  product: string;
  online: boolean;
  dps: DataPoint[] | null;
  /** Why the last command to it failed, if that's recent. */
  error?: string;
  /** The integration's name for it, when the owner gave it another. */
  originalName?: string;
}

export type Rooms = Record<string, string[]>;

/** The home's own settings. An empty language or timezone isn't chosen yet; no location is both null. */
export interface HomeSettings {
  language: string;
  timezone: string;
  latitude: number | null;
  longitude: number | null;
}

/** One thing a script asked to reach. Until approved, that call is refused and the rest of the script keeps running. */
export interface ScriptAccess {
  key: string;
  label: string;
  approved: boolean;
  refused?: number;
}

export interface ScriptStatus {
  name: string;
  live: boolean;
  error?: string;
  access: ScriptAccess[];
}

export interface AutomationEvent {
  time: string;
  script: string;
  kind: string;
  message: string;
}

export interface PluginStatus {
  id: string;
  type: string;
  data?: unknown;
  updatedAt?: string;
  error?: string;
  actions?: string[];
}

export interface ExtensionSetting {
  key: string;
  label: string;
  type: "string" | "number" | "boolean" | "secret" | "file";
  required: boolean;
  help?: string;
  default?: string | number | boolean;
  set: boolean;
  /** What a setting that isn't a secret or a file is set to. Never present for those. */
  value?: string | number | boolean;
}

/** A secret or a file's contents: never shown again once given, and kept when left empty. */
export const hiddenSetting = (s: Pick<ExtensionSetting, "type">) => s.type === "secret" || s.type === "file";

export interface IntegrationDevice {
  id: string;
  name: string;
  kind: string;
  online: boolean;
}

export interface Integration {
  type: string;
  name: string;
  description?: string;
  devices: IntegrationDevice[];
  extension?: ExtensionStatus;
}

export interface FoundDevice {
  id: string;
  name: string;
  product?: string;
  address?: string;
  /** What the owner has to do before it can be used. */
  note?: string;
  state: "added" | "removed" | "new";
  seen: boolean;
}

export interface Discovery {
  supported: boolean;
  error?: string;
  devices: FoundDevice[];
}

export interface ExtensionActivity {
  kind: string;
  destination: string;
  count: number;
  first: string;
  last: string;
}

export interface Violation {
  time: string;
  extension: string;
  kind: string;
  detail: string;
}

export interface UpgradeStep {
  version: string;
  note: string;
}

/** What moving to an inspected version asks of the person. A secret's value is never here. */
export interface Upgrade {
  from: string;
  to: string;
  steps: UpgradeStep[];
  ask: ExtensionSetting[];
}

export interface ExtensionStatus {
  source: string;
  id?: string;
  name?: string;
  version?: string;
  description?: string;
  homepage?: string;
  access: string[];
  needsApproval?: boolean;
  warnings?: string[];
  risks?: string[];
  violations?: Violation[];
  activity?: ExtensionActivity[];
  settings: ExtensionSetting[];
  provides: string[];
  running: boolean;
  error?: string;
  /** The path of a webhook address that was just made. It comes back once, in the answer to the call that made it. */
  webhook?: string;
  /** Present when what was inspected would update an extension added on the page. */
  upgrade?: Upgrade;
  /** The marketplace took this version back. It keeps running until the owner decides otherwise. */
  revoked?: MarketRevocation;
  /** A newer version of this marketplace copy, with what each version on the way says. */
  marketUpdate?: { version: string; steps?: (UpgradeStep & { ask?: string[] })[] };
}

export interface MarketRevocation {
  id: string;
  version: string;
  state: "withdrawn" | "suspended";
  reason: string;
}

/** One image of a listing. The address goes through Tomtly, never to the marketplace. */
export interface MarketAsset {
  name: string;
  type: string;
  sha256: string;
}

/** Not signed: only as good as what the marketplace says. Zero means there are none. */
export interface MarketRating {
  average: number;
  count: number;
}

export interface MarketReview {
  id: string;
  rating: number;
  text: string;
  name: string;
  at: string;
}

export interface MarketReviews extends MarketRating {
  reviews: MarketReview[];
}

export interface MarketEntry {
  id: string;
  name: string;
  description: string;
  kind: "integration" | "plugin" | "both";
  version: string;
  author: string;
  homepage?: string;
  /** The SPDX id of its open license. Older entries have none. */
  license?: string;
  provides: string[];
  access: string[];
  testedOn: string;
  approvedOn: string;
  size: number;
  /** The hash of the package that was verified, which is what anyone installing it gets. */
  sha256: string;
  /** This listing's own marketplace copy is installed. */
  installed: boolean;
  /** Something else has this id: an upload, a GitHub repo or a folder. */
  differentCopy?: boolean;
  /** Why new installs are stopped for now. */
  suspended?: string;
  installedVersion?: string;
  /** The source of the installed copy, which an update replaces. */
  installedSource?: string;
  updateAvailable: boolean;
  /** The author's own link, which nothing checks. */
  repository?: string;
  /** What an update goes through. ask holds setting keys: the labels come with the download. */
  upgradeSteps?: (UpgradeStep & { ask?: string[] })[];
  icon?: MarketAsset;
  screenshots?: MarketAsset[];
  rating?: MarketRating;
  /** From the fixed list; older listings have none. */
  categories?: string[];
  /** Brands and devices the author says it works with. */
  tags?: string[];
  /** Not signed, and counts reinstalls too. */
  downloads?: number;
}

/** verified is false when the marketplace answered but its list can't be trusted, so nothing is offered. */
export interface Marketplace {
  configured: boolean;
  reachable: boolean;
  verified: boolean;
  error?: string;
  entries: MarketEntry[];
  /** The marketplace's address; its website, where people browse and choose Get, is at its root. */
  site?: string;
}

export interface ScriptSource {
  name: string;
  source: string;
  version: string;
  access?: ScriptAccess[];
}

export interface AutomationStatus {
  scripts: ScriptStatus[];
  plugins?: PluginStatus[];
  events: AutomationEvent[];
}

export type ApiError = Error & { status: number };

/** The shell sets onExpired; every call that comes back 401 reports it, so one place decides what a lost session looks like. */
export const session = { expired: false, onExpired: () => {} };

export async function call<T>(path: string, init?: RequestInit, opts: { fallback?: string; authed?: boolean } = {}): Promise<T> {
  const res = await fetch(path, init);
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    // Login and setup answer 401 for a wrong password; that isn't a lost session.
    if (res.status === 401 && opts.authed !== false) session.onExpired();
    throw Object.assign(new Error(data?.error ?? opts.fallback ?? `Request failed with status ${res.status}`), { status: res.status });
  }
  return data as T;
}

export function request<T>(path: string, body?: unknown, method = body === undefined ? "GET" : "POST", opts?: { authed?: boolean }): Promise<T> {
  const init: RequestInit = method === "GET" ? {} : { method, headers: { "Content-Type": "application/json" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) };
  return call<T>(path, init, opts);
}

export type WidgetTone = "good" | "warn" | "bad";

/** One piece of a widget, as sdk/home.d.ts describes it. What a field means depends on type. */
export interface WidgetBlock {
  type: "group" | "text" | "stat" | "rows" | "device" | "number" | "toggle" | "select" | "time" | "button" | "dialog";
  label?: string;
  text?: string;
  value?: Value | null;
  unit?: string;
  tone?: WidgetTone;
  rows?: { label: string; value?: Value | null; tone?: WidgetTone }[];
  device?: string;
  code?: string;
  name?: string;
  min?: number;
  max?: number;
  step?: number;
  options?: string[];
  action?: string;
  args?: Record<string, unknown>;
  /** A dialog's heading, the form it opens, and how many inputs sit side by side in it. */
  title?: string;
  blocks?: WidgetBlock[];
  columns?: number;
}

/** A widget a script or an extension offers, as last drawn. */
export interface RenderedWidget {
  /** "<owner>/<key>": owner is builtin, script:<name> or ext:<id>. */
  key: string;
  title: string;
  /** The script's or extension's name. */
  from: string;
  blocks?: WidgetBlock[];
  error?: string;
  /** A script in shadow mode: a button only logs what it would do. */
  practising?: boolean;
}

export interface Widgets {
  /** What's on Home, in order. */
  layout: string[];
  widgets: RenderedWidget[];
}

export const api = {
  widgets: () => request<Widgets>("/api/widgets"),
  setDashboard: (widgets: string[]) => request<{ layout: string[] }>("/api/dashboard", { widgets }, "PUT"),
  /** Presses a button on a script's or extension's widget, with what its inputs hold. live is false for a practising script. */
  pressWidget: (key: string, action: string, input: Record<string, unknown>) => {
    const at = key.indexOf("/");
    const owner = encodeURIComponent(key.slice(0, at)), name = encodeURIComponent(key.slice(at + 1));
    return request<{ live: boolean }>(`/api/widgets/${owner}/${name}/actions/${encodeURIComponent(action)}`, { input });
  },
  devices: () => request<Device[]>("/api/devices"),
  rooms: () => request<Rooms>("/api/rooms"),
  settings: () => request<HomeSettings>("/api/settings"),
  setSettings: (s: HomeSettings) => request<HomeSettings>("/api/settings", s, "PUT"),
  /** The values pinned to each device's card, by device id. */
  pins: () => request<Record<string, string[]>>("/api/pins"),
  /** An empty name goes back to the integration's, a null room to config.json's, and an empty room is no room. */
  editDevice: (id: string, change: { name?: string; room?: string | null }) => request<unknown>(`/api/devices/${encodeURIComponent(id)}`, change, "PATCH"),
  addRoom: (name: string) => request<Rooms>("/api/rooms", { name }),
  /** position counts from 0, at the top. */
  editRoom: (name: string, change: { name?: string; position?: number }) => request<Rooms>(`/api/rooms/${encodeURIComponent(name)}`, change, "PATCH"),
  deleteRoom: (name: string) => request<Rooms>(`/api/rooms/${encodeURIComponent(name)}`, undefined, "DELETE"),
  setPins: (id: string, dps: string[]) => request<{ dps: string[] }>(`/api/devices/${encodeURIComponent(id)}/pins`, { dps }, "PUT"),
  write: (id: string, dp: string, value: Value) =>
    request(`/api/devices/${encodeURIComponent(id)}/dps/${encodeURIComponent(dp)}`, { value }),
  automations: () => request<AutomationStatus>("/api/automations"),
  extensions: () => request<ExtensionStatus[]>("/api/extensions"),
  integrations: () => request<Integration[]>("/api/integrations"),
  discoverDevices: (id: string) => request<Discovery>(`/api/integrations/${encodeURIComponent(id)}/discover`, undefined, "POST"),
  removeDevice: (id: string) => request<unknown>(`/api/devices/${encodeURIComponent(id)}`, undefined, "DELETE"),
  restoreDevice: (id: string) => request<unknown>(`/api/devices/${encodeURIComponent(id)}/restore`, undefined, "POST"),
  marketplace: () => request<Marketplace>("/api/marketplace"),
  inspectMarketEntry: (id: string) => request<ExtensionStatus>("/api/marketplace/inspect", { id }),
  marketReviews: (id: string, offset = 0) => request<MarketReviews>(`/api/marketplace/${encodeURIComponent(id)}/reviews?offset=${offset}`),
  postMarketReview: (id: string, review: { rating: number; text: string; name: string }) => request<unknown>(`/api/marketplace/${encodeURIComponent(id)}/reviews`, review),
  inspectExtension: (source: string) => request<ExtensionStatus>("/api/extensions/inspect", { source }),
  // access is what the person was shown, and anything else is refused: a definition can answer differently each time it runs.
  addExtension: (source: string, settings: Record<string, unknown>, access: string[]) => request<ExtensionStatus>("/api/extensions", { source, settings, access }),
  // The type is not a plain form's, so other websites can't send files here.
  uploadExtension: (file: File) =>
    call<ExtensionStatus>(`/api/extensions/upload?name=${encodeURIComponent(file.name)}`, {
      method: "POST",
      headers: { "Content-Type": "application/octet-stream" },
      body: file,
    }, { fallback: "Upload failed" }),
  approveExtension: (source: string, access: string[]) => request<{ approved: boolean; webhook?: string }>("/api/extensions/approve", { source, access }),
  stopExtension: (source: string) => request<unknown>("/api/extensions/stop", { source }),
  updateExtensionSettings: (source: string, settings: Record<string, unknown>) => request<ExtensionStatus>("/api/extensions/settings", { source, settings }),
  replaceExtension: (source: string, newSource: string, settings: Record<string, unknown> | undefined, access: string[]) => request<ExtensionStatus>("/api/extensions/replace", { source, newSource, settings, access }),
  newWebhookAddress: (source: string) => request<{ webhook: string }>("/api/extensions/webhook-token", { source }),
  removeExtension: (source: string) => request<unknown>(`/api/extensions?source=${encodeURIComponent(source)}`, undefined, "DELETE"),
  setLive: (name: string, live: boolean) =>
    request<AutomationStatus>(`/api/automations/${encodeURIComponent(name)}/live`, { live }),
  setAccess: (name: string, keys: string[], approved: boolean) =>
    request<AutomationStatus>(`/api/automations/${encodeURIComponent(name)}/access`, { keys, approved }),
  reload: () => request<AutomationStatus>("/api/automations/reload", {}),
  source: (name: string) => request<ScriptSource>(`/api/automations/${encodeURIComponent(name)}/source`),
  saveSource: (name: string, source: string, version: string, keepLive: boolean) =>
    request<ScriptSource>(`/api/automations/${encodeURIComponent(name)}/source`, { source, version, keepLive }),
  sdk: () => request<Record<string, string>>("/api/automations/sdk"),
};
