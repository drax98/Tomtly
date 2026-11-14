import { useEffect, useState, type ReactNode } from "react";
import { ArrowUpCircle, BatteryLow, Ban, CircleAlert, CircleCheck, Flame, ScrollText, ShieldAlert, Thermometer, TriangleAlert, Wifi, WifiOff, X } from "lucide-react";
import { session, type AutomationStatus, type Device, type ExtensionStatus, type Rooms } from "@/api";
import type { Permission } from "@/authApi";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { display, findDp } from "@/lib/device";
import { navigate, type Page } from "@/lib/route";
import { cn } from "@/lib/utils";

const LOW_BATTERY = 20;
const RECENT_MS = 24 * 60 * 60 * 1000;

export const WIDGETS_POLL_MS = 5_000; // scripts draw their widgets every 5 seconds

/** Asks again every so often, and stops while signed out. A failure keeps the last answer. */
export function usePoll<T>(load: (() => Promise<T>) | null, everyMs: number) {
  const [data, setData] = useState<T | null>(null);
  useEffect(() => {
    if (!load) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      if (!session.expired) {
        try {
          const next = await load();
          if (!stopped) setData(next);
        } catch {
          // The rest of the page still works.
        }
      }
      if (!stopped) timer = setTimeout(tick, everyMs);
    };
    void tick();
    return () => { stopped = true; clearTimeout(timer); };
  }, [load, everyMs]);
  return [data, setData] as const;
}

export interface Problem {
  key: string;
  /** This occurrence: the same problem with another detail or a newer event is a new one, so clearing doesn't hide it. */
  stamp: string;
  icon: ReactNode;
  text: string;
  detail?: string;
  page: Page;
  sub?: string;
  tone: "warn" | "bad";
}

const time = (iso: string) => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const today = new Date().toDateString() === d.toDateString();
  return d.toLocaleString(undefined, today ? { hour: "2-digit", minute: "2-digit" } : { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
};

/** Everything on the home that wants a person, most serious first. Empty on a good day. */
export function problems(devices: Device[], extensions: ExtensionStatus[] | null, automations: AutomationStatus | null, now = Date.now()): Problem[] {
  const out: Problem[] = [];
  for (const e of extensions ?? []) {
    const name = e.name ?? e.id ?? e.source;
    const page: Page = e.provides.includes("devices") ? "integrations" : "plugins";
    if (e.violations?.length) out.push({ stamp: "", key: `v-${e.source}`, icon: <Ban />, text: `${name} tried something it isn't allowed to`, detail: `Blocked ${e.violations.length === 1 ? "1 attempt" : `${e.violations.length} attempts`}`, page, tone: "bad" });
    if (e.revoked) out.push({ stamp: "", key: `r-${e.source}`, icon: <ShieldAlert />, text: `The marketplace ${e.revoked.state === "withdrawn" ? "withdrew" : "suspended"} ${name}`, detail: e.revoked.reason, page, tone: "bad" });
    if (e.needsApproval) out.push({ stamp: "", key: `a-${e.source}`, icon: <ShieldAlert />, text: `${name} is waiting for your OK`, page, tone: "warn" });
    else if (e.error) out.push({ stamp: "", key: `e-${e.source}`, icon: <CircleAlert />, text: `${name} has a problem`, detail: e.error, page, tone: "bad" });
    if (e.marketUpdate) out.push({ stamp: "", key: `u-${e.source}`, icon: <ArrowUpCircle />, text: `${name} ${e.marketUpdate.version} is available`, page, tone: "warn" });
  }
  for (const s of automations?.scripts ?? []) {
    if (s.error) out.push({ stamp: "", key: `s-${s.name}`, icon: <ScrollText />, text: `The ${s.name} automation doesn't run`, detail: s.error, page: "automations", sub: s.name, tone: "bad" });
  }
  for (const d of devices) {
    if (d.error) out.push({ stamp: "", key: `c-${d.id}`, icon: <TriangleAlert />, text: `A command to ${d.name} failed`, detail: d.error, page: "devices", tone: "bad" });
    if (!d.online) out.push({ stamp: "", key: `o-${d.id}`, icon: <WifiOff />, text: `${d.name} is offline`, detail: "Check it has power and Wi-Fi.", page: "devices", tone: "warn" });
    const battery = findDp(d, "battery", "battery_percentage")?.value;
    if (d.online && typeof battery === "number" && battery <= LOW_BATTERY) out.push({ stamp: "", key: `b-${d.id}`, icon: <BatteryLow />, text: `${d.name}'s battery is low`, detail: `${battery}%`, page: "devices", tone: "warn" });
  }
  const mismatched = new Set<string>();
  for (const ev of automations?.events ?? []) {
    if (ev.kind !== "mismatch" || mismatched.has(ev.script) || now - new Date(ev.time).getTime() > RECENT_MS) continue;
    mismatched.add(ev.script);
    out.push({ stamp: `m-${ev.script}|${ev.time}`, key: `m-${ev.script}`, icon: <TriangleAlert />, text: `A device isn't doing what ${ev.script} wants`, detail: ev.message, page: "automations", sub: ev.script, tone: "warn" });
  }
  for (const p of out) if (!p.stamp) p.stamp = `${p.key}|${p.detail ?? ""}`;
  return out.sort((a, b) => (a.tone === b.tone ? 0 : a.tone === "bad" ? -1 : 1));
}

/** When each cleared problem was cleared, by stamp. Kept in this browser only. */
export type Cleared = Record<string, number>;
const CLEARED_KEY = "needsYouCleared";
/** A cleared problem that is still there comes back after this, so nothing stays hidden for good. */
export const CLEARED_FOR_MS = 24 * 60 * 60 * 1000;

function loadCleared(): Cleared {
  try {
    const v = JSON.parse(localStorage.getItem(CLEARED_KEY) ?? "{}");
    return v && typeof v === "object" ? v : {};
  } catch {
    return {};
  }
}

function saveCleared(c: Cleared) {
  try {
    localStorage.setItem(CLEARED_KEY, JSON.stringify(c));
  } catch {
    // Only a convenience; without it cleared problems come back on the next visit.
  }
}

/**
 * Which problems to show, and which clearings to keep. A clearing is forgotten once its problem is gone, so the same
 * problem coming back later shows again, and after CLEARED_FOR_MS. Gone only counts once that kind of data has loaded
 * (by the key's first letter: devices, extensions or automations), so a page still loading forgets nothing.
 */
export function sortCleared(list: Problem[], cleared: Cleared, loaded: { devices: boolean; extensions: boolean; automations: boolean }, now = Date.now()) {
  const present = new Set(list.map((p) => p.stamp));
  const source = (stamp: string) => ("vraeu".includes(stamp[0]) ? loaded.extensions : "sm".includes(stamp[0]) ? loaded.automations : loaded.devices);
  const keep: Cleared = {};
  for (const [stamp, at] of Object.entries(cleared)) {
    if (now - at >= CLEARED_FOR_MS) continue;
    if (!present.has(stamp) && source(stamp)) continue;
    keep[stamp] = at;
  }
  return { shown: list.filter((p) => !(p.stamp in keep)), keep };
}

export function Go({ page, sub, children }: { page: Page; sub?: string; children: ReactNode }) {
  const href = sub ? `/${page}/${encodeURIComponent(sub)}` : `/${page}`;
  return <a className="text-sm underline-offset-4 hover:underline" href={href} onClick={(e) => { e.preventDefault(); navigate(page, sub); }}>{children}</a>;
}

/** What the built-in widgets draw from. Null is not known yet, or not this person's to see. */
export interface HomeData {
  devices: Device[];
  rooms: Rooms;
  extensions: ExtensionStatus[] | null;
  automations: AutomationStatus | null;
}

// The page calls each built-in's Body as a function, so the state lives in a component of its own.
function NeedsYou(data: HomeData) {
  return <NeedsYouList {...data} />;
}

function NeedsYouList({ devices, extensions, automations }: HomeData) {
  const [cleared, setCleared] = useState<Cleared>(loadCleared);
  const list = problems(devices, extensions, automations);
  const { shown, keep } = sortCleared(list, cleared, { devices: devices.length > 0, extensions: extensions !== null, automations: automations !== null });
  const forgotten = Object.keys(keep).length !== Object.keys(cleared).length;
  useEffect(() => {
    if (!forgotten) return;
    setCleared(keep);
    saveCleared(keep);
  }, [forgotten, keep]);
  const clear = (ps: Problem[]) => {
    const next = { ...keep };
    for (const p of ps) next[p.stamp] = Date.now();
    setCleared(next);
    saveCleared(next);
  };
  if (shown.length === 0) {
    return (
      <p className="flex items-center gap-2 text-sm text-good">
        <CircleCheck aria-hidden className="size-4" />
        {list.length === 0 ? "All good. Nothing needs you." : "Nothing new needs you. What you cleared comes back if it happens again."}
      </p>
    );
  }
  return (
    <div className="space-y-1">
      <ul className="divide-y divide-border/60" aria-label="What needs you">
        {shown.map((p) => (
          <li key={p.key} className="flex items-start gap-3 py-2.5">
            <span aria-hidden className={cn("mt-0.5 shrink-0 [&>svg]:size-4", p.tone === "bad" ? "text-destructive" : "text-warn")}>{p.icon}</span>
            <span className="min-w-0 flex-1 text-sm">
              <span className="block font-medium">{p.text}</span>
              {p.detail && <span className="block break-words text-xs text-muted-foreground">{p.detail}</span>}
            </span>
            <Go page={p.page} sub={p.sub}>Open</Go>
            <Button variant="ghost" size="icon" className="-my-1.5 size-8 shrink-0" aria-label={`Clear: ${p.text}`} title="Clear. It comes back if it happens again." onClick={() => clear([p])}>
              <X aria-hidden className="size-4" />
            </Button>
          </li>
        ))}
      </ul>
      {shown.length > 1 && (
        <div className="flex justify-end">
          <Button variant="ghost" size="sm" onClick={() => clear(shown)}>Clear all</Button>
        </div>
      )}
    </div>
  );
}

function Online({ devices }: HomeData) {
  const online = devices.filter((d) => d.online).length;
  return (
    <>
      <p className="text-reading font-semibold tabular-nums">{online} <span className="text-base font-normal text-muted-foreground">of {devices.length} online</span></p>
      {devices.length > 0 && (
        <div className="mt-2 h-2 overflow-hidden rounded-full bg-secondary" aria-hidden>
          <div className="h-full rounded-full bg-good" style={{ width: `${(online / devices.length) * 100}%` }} />
        </div>
      )}
    </>
  );
}

function Automations({ automations }: HomeData) {
  if (automations === null) return <Skeleton className="h-16" />;
  if (automations.scripts.length === 0) return <p className="text-sm text-muted-foreground">None yet.</p>;
  const live = automations.scripts.filter((s) => s.live && !s.error).length;
  const practising = automations.scripts.filter((s) => !s.live && !s.error).length;
  const broken = automations.scripts.filter((s) => s.error).length;
  const recent = automations.events.filter((ev) => ["set", "shadow", "error", "mismatch", "matched", "refused"].includes(ev.kind)).slice(0, 8);
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-2">
        <Badge variant="secondary" className="bg-good/15 text-good">{live} live</Badge>
        <Badge variant="secondary">{practising} practising</Badge>
        {broken > 0 && <Badge variant="secondary" className="bg-destructive/15 text-destructive">{broken} not running</Badge>}
      </div>
      {recent.length > 0 ? (
        <ul aria-label="What they did lately" className="space-y-1.5 text-sm">
          {recent.map((ev, i) => (
            <li key={`${ev.time}-${i}`} className="flex gap-2">
              <span className="w-24 shrink-0 tabular-nums text-xs leading-5 text-muted-foreground">{time(ev.time)}</span>
              <span className="min-w-0 break-words">
                <span className="font-medium">{ev.script}</span>{" "}
                <span className={cn(ev.kind === "error" && "text-destructive", ev.kind === "mismatch" && "text-warn", ev.kind === "shadow" && "text-muted-foreground")}>{ev.message}</span>
              </span>
            </li>
          ))}
        </ul>
      ) : <p className="text-sm text-muted-foreground">Nothing done lately.</p>}
    </div>
  );
}

function Heating({ devices, rooms }: HomeData) {
  const roomOf = (id: string) => Object.entries(rooms).find(([, ids]) => ids.includes(id))?.[0];
  const climate = devices.filter((d) => d.kind === "climate" && findDp(d, "temp_current"))
    .sort((a, b) => (roomOf(a.id) ?? "￿").localeCompare(roomOf(b.id) ?? "￿") || a.name.localeCompare(b.name));
  if (climate.length === 0) return <p className="text-sm text-muted-foreground">Nothing here reports a temperature yet.</p>;
  return (
    <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" aria-label="Heating now">
      {climate.map((d) => {
        const current = findDp(d, "temp_current");
        const target = findDp(d, "temp_set");
        const heating = findDp(d, "heating_state")?.value === true && findDp(d, "switch", "power")?.value !== false;
        return (
          <li key={d.id} className={cn("flex items-center justify-between gap-3 rounded-lg border px-3 py-2.5", heating && "border-heat/40 bg-heat/[0.08]", !d.online && "opacity-60")}>
            <span className="min-w-0">
              <span className="block truncate text-sm font-medium">{roomOf(d.id) ?? d.name}</span>
              {roomOf(d.id) && <span className="block truncate text-xs text-muted-foreground">{d.name}</span>}
            </span>
            <span className="shrink-0 text-right">
              <span className="block text-lg font-semibold tabular-nums">{current ? display(current) : "–"}</span>
              <span className={cn("flex items-center justify-end gap-1 text-xs", heating ? "text-heat" : "text-muted-foreground")}>
                {heating && <Flame aria-hidden className="size-3" />}
                {!d.online ? "Offline" : heating ? `Heating to ${target ? display(target) : "…"}` : target ? `Holding ${display(target)}` : "Idle"}
              </span>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** A widget Tomtly draws itself. */
export interface Builtin {
  key: string;
  title: string;
  description: string;
  icon: ReactNode;
  /** Takes the whole width on a wide screen. */
  wide?: boolean;
  /** What someone needs to see it. */
  needs?: Permission;
  link?: { page: Page; text: string };
  Body: (data: HomeData) => ReactNode;
}

export const BUILTINS: Builtin[] = [
  { key: "builtin/needs-you", title: "Needs you", description: "Offline devices, low batteries, failed commands, and extensions or automations with a problem.", icon: <CircleAlert />, wide: true, Body: NeedsYou },
  { key: "builtin/devices", title: "Devices", description: "How many devices are online.", icon: <Wifi />, link: { page: "devices", text: "All devices" }, Body: Online },
  { key: "builtin/automations", title: "Automations", description: "Which run live or practise, and what they did lately.", icon: <ScrollText />, needs: "automations.read", link: { page: "automations", text: "All automations" }, Body: Automations },
  { key: "builtin/heating", title: "Heating now", description: "Each room's temperature, and whether it's heating.", icon: <Thermometer />, wide: true, Body: Heating },
];
