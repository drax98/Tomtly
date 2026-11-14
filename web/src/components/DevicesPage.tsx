import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { Lightbulb, RefreshCw, Search, WifiOff } from "lucide-react";
import { api, type Device, type Rooms, type Value } from "@/api";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup } from "@/components/ToggleGroup";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { cardFor } from "@/components/cards";
import { OpenDevice, Pinned, WriteAccess } from "@/components/controls";
import { DeviceDrawer } from "@/components/DeviceDrawer";
import { groupByKind, groupByRoom, matches, summarize } from "@/lib/device";
import { useAuth } from "@/lib/auth";
import { messageOf } from "@/lib/utils";

type GroupBy = "room" | "kind";

function loadGroupBy(): GroupBy {
  try {
    return localStorage.getItem("groupBy") === "kind" ? "kind" : "room";
  } catch {
    return "room";
  }
}

const slug = (title: string) => `group-${title.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`;

interface Props {
  devices: Device[] | null;
  rooms: Rooms;
  reachable: boolean;
  write: (id: string, dp: string, value: Value, track?: boolean) => void;
  /** A device was renamed or moved, so devices and rooms are worth asking for again. */
  onEdited?: () => void;
}

export function DevicesPage({ devices, rooms, reachable, write, onEdited }: Props) {
  const { can } = useAuth();
  const [groupBy, setGroupBy] = useState<GroupBy>(loadGroupBy);
  const [query, setQuery] = useState("");
  const [opened, setOpened] = useState<string | null>(null);
  const [pins, setPins] = useState<Record<string, string[]>>({});
  useEffect(() => {
    let stale = false;
    // Without pins the cards still work, so a failure here only costs them.
    api.pins().then((p) => !stale && setPins(p), () => {});
    return () => { stale = true; };
  }, []);

  const changePins = async (id: string, dps: string[]) => {
    const before = pins[id] ?? [];
    setPins((all) => ({ ...all, [id]: dps }));
    try {
      await api.setPins(id, dps);
    } catch (e) {
      setPins((all) => ({ ...all, [id]: before }));
      toast.error("The pin wasn't saved", { description: messageOf(e, "Try again.") });
    }
  };

  const changeGroupBy = (next: GroupBy) => {
    setGroupBy(next);
    try {
      localStorage.setItem("groupBy", next);
    } catch {
      // Only a convenience; the page works without it.
    }
  };

  const hasRooms = Object.keys(rooms).length > 0;
  const roomOf = useMemo(() => {
    const map = new Map<string, string>();
    for (const [room, ids] of Object.entries(rooms)) for (const id of ids) map.set(id, room);
    return map;
  }, [rooms]);

  const visible = useMemo(() => devices?.filter((d) => matches(d, roomOf.get(d.id) ?? "", query)) ?? null, [devices, roomOf, query]);
  const groups = visible ? (groupBy === "room" && hasRooms ? groupByRoom(visible, rooms) : groupByKind(visible)) : [];
  const offline = devices?.filter((d) => !d.online).length ?? 0;
  const openedDevice = opened ? devices?.find((d) => d.id === opened) : undefined;

  return (
    <WriteAccess.Provider value={can("devices.write")}>
      <OpenDevice.Provider value={setOpened}>
      <div className="space-y-6">
        <PageHeader
          title="Your devices"
          description={can("devices.write") ? "Tap a switch to turn something on or off. Changes show up straight away." : "You can see everything here, but only change things with the Family role or higher."}
        />

        {!reachable && (
          <Alert variant="warning">
            <WifiOff aria-hidden />
            <AlertTitle>Can't reach the home service</AlertTitle>
            <AlertDescription>
              What you see may be out of date, and changes won't be sent. It keeps trying every few seconds; if this lasts, check the computer running Tomtly is on.
            </AlertDescription>
          </Alert>
        )}

        {devices && devices.length > 0 && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-3">
              <div className="relative min-w-56 flex-1 sm:max-w-sm">
                <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
                <Input type="search" aria-label="Find a device" placeholder="Find a device or room" className="pl-9" value={query} onChange={(e) => setQuery(e.target.value)} />
              </div>
              {hasRooms && (
                <ToggleGroup
                  label="Group devices"
                  value={groupBy}
                  onChange={changeGroupBy}
                  options={[["room", "By room"], ["kind", "By type"]]}
                />
              )}
            </div>
            {groups.length > 1 && (
              <nav aria-label="Jump to" className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1 [scrollbar-width:none] sm:mx-0 sm:flex-wrap sm:px-0">
                {groups.map((g) => (
                  <a key={g.title} href={`#${slug(g.title)}`} onClick={(e) => { e.preventDefault(); document.getElementById(slug(g.title))?.scrollIntoView({ behavior: "smooth", block: "start" }); }}
                    className="inline-flex min-h-9 shrink-0 items-center rounded-full border bg-card px-3.5 text-sm transition-colors hover:bg-accent">
                    {g.title}
                  </a>
                ))}
              </nav>
            )}
          </div>
        )}

        {devices === null && reachable && (
          <div className="grid items-start gap-4 sm:grid-cols-2 xl:grid-cols-3" aria-busy="true" aria-label="Loading devices">
            {[0, 1, 2, 3, 4, 5].map((i) => <Skeleton key={i} className="h-44 rounded-xl" />)}
          </div>
        )}

        {devices?.length === 0 && (
          <EmptyState icon={<Lightbulb />} title="No devices yet" action={<Button variant="outline" onClick={() => window.location.reload()}><RefreshCw aria-hidden />Check again</Button>}>
            Tomtly shows what your integrations find. Add one on the Integrations page, then check again.
          </EmptyState>
        )}

        {devices && devices.length > 0 && visible?.length === 0 && (
          <EmptyState icon={<Search />} title={`Nothing matches "${query}"`} action={<Button variant="outline" onClick={() => setQuery("")}>Clear the search</Button>}>
            Try a room, a device name or a type like "light".
          </EmptyState>
        )}

        {offline > 0 && !query && (
          <p className="text-sm text-muted-foreground">{offline} {offline === 1 ? "device is" : "devices are"} offline. Its card says so; check its power and Wi-Fi.</p>
        )}

        {groups.map((group) => (
          <section key={group.title} id={slug(group.title)} aria-labelledby={`${slug(group.title)}-h`} className="scroll-mt-20 space-y-3">
            <div className="flex flex-wrap items-baseline gap-x-3">
              <h2 id={`${slug(group.title)}-h`} className="text-lg font-semibold">{group.title}</h2>
              <p className="text-sm text-muted-foreground">{summarize(group.devices)}</p>
            </div>
            <div className="grid items-start gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {group.devices.map((device) => {
                const Card = cardFor(device);
                return (
                  <div key={device.id} className="min-w-0 space-y-1">
                    <Pinned.Provider value={pins[device.id] ?? []}>
                      <Card device={device} write={write} />
                    </Pinned.Provider>
                    {device.error && <p role="alert" className="break-words px-1 text-xs font-medium text-destructive">The last command failed: {device.error}</p>}
                  </div>
                );
              })}
            </div>
          </section>
        ))}
      </div>
      {openedDevice && (
        <DeviceDrawer
          device={openedDevice}
          devices={devices ?? []}
          room={roomOf.get(openedDevice.id)}
          pinned={pins[openedDevice.id] ?? []}
          onPins={can("devices.write") ? (dps) => void changePins(openedDevice.id, dps) : undefined}
          rooms={Object.keys(rooms)}
          onEdited={can("extensions.manage") ? onEdited ?? (() => {}) : undefined}
          write={write}
          onClose={() => setOpened(null)}
        />
      )}
      </OpenDevice.Provider>
    </WriteAccess.Provider>
  );
}
