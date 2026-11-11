import { useState, type FormEvent } from "react";
import { DoorOpen, GripVertical, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api, type Device, type Rooms } from "@/api";
import { useConfirm } from "@/components/ConfirmDialog";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuth } from "@/lib/auth";
import { useDragOrder, type Drag } from "@/lib/useDragOrder";
import { cn, messageOf } from "@/lib/utils";

interface Props {
  devices: Device[] | null;
  rooms: Rooms;
  /** Rooms or devices changed, so both are worth asking for again. */
  onChanged: () => void;
}

const fail = (title: string) => (e: unknown) => toast.error(title, { description: messageOf(e, "Try again.") });

/** Ticks the devices in one room. Ticking one moves it here from wherever it was; unticking leaves it in no room. */
function ChooseDevices({ room, devices, rooms, onClose, onChanged }: { room: string; devices: Device[]; rooms: Rooms; onClose: () => void; onChanged: () => void }) {
  const roomOf = (id: string) => Object.entries(rooms).find(([, ids]) => ids.includes(id))?.[0];
  const [busy, setBusy] = useState<string | null>(null);
  const toggle = async (d: Device, inHere: boolean) => {
    setBusy(d.id);
    try {
      await api.editDevice(d.id, { room: inHere ? "" : room });
      onChanged();
    } catch (e) {
      fail(`Couldn't move ${d.name}`)(e);
    } finally {
      setBusy(null);
    }
  };
  const sorted = [...devices].sort((a, b) => a.name.localeCompare(b.name));
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Devices in {room}</DialogTitle>
          <DialogDescription>A device is in one room at a time, so ticking one moves it here.</DialogDescription>
        </DialogHeader>
        <ul className="divide-y divide-border/60">
          {sorted.map((d) => {
            const where = roomOf(d.id);
            const inHere = where === room;
            return (
              <li key={d.id}>
                <label className="flex min-h-11 cursor-pointer items-center gap-3 py-2 text-sm">
                  <input type="checkbox" className="size-4 accent-primary" checked={inHere} disabled={busy !== null} onChange={() => void toggle(d, inHere)} />
                  <span className="min-w-0 flex-1 break-words">{d.name}</span>
                  {where && !inHere && <span className="shrink-0 text-xs text-muted-foreground">in {where}</span>}
                </label>
              </li>
            );
          })}
        </ul>
      </DialogContent>
    </Dialog>
  );
}

function RoomCard({ name, count, devices, rooms, manage, onChanged, drag }: { name: string; count: number; devices: Device[]; rooms: Rooms; manage: boolean; onChanged: () => void; drag: Drag }) {
  const confirm = useConfirm();
  const [renaming, setRenaming] = useState(false);
  const [draft, setDraft] = useState(name);
  const [choosing, setChoosing] = useState(false);
  const inside = devices.filter((d) => rooms[name]?.includes(d.id));
  const edit = async (change: { name?: string; position?: number }) => {
    try {
      await api.editRoom(name, change);
      onChanged();
      return true;
    } catch (e) {
      fail("Couldn't change the room")(e);
      return false;
    }
  };
  const rename = async (e: FormEvent) => {
    e.preventDefault();
    if (draft.trim() === name || (await edit({ name: draft.trim() }))) setRenaming(false);
  };
  const remove = async () => {
    const ok = await confirm({
      title: `Delete ${name}?`,
      body: <p>{inside.length ? `Its ${inside.length === 1 ? "device stays" : `${inside.length} devices stay`}, in no room. Automations aren't affected: they use devices, not rooms.` : "It has no devices."}</p>,
      action: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteRoom(name);
      onChanged();
    } catch (e) {
      fail("Couldn't delete the room")(e);
    }
  };
  return (
    <Card
      data-testid={`room-${name}`} ref={drag.cardRef} onPointerDown={manage && !renaming ? drag.onPointerDown : undefined}
      className={cn(manage && !renaming && "cursor-grab select-none", drag.dragging && "relative z-10 cursor-grabbing opacity-70 shadow-lg ring-2 ring-ring")}
    >
      <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
        {manage && !renaming && (
          <Button
            type="button" variant="ghost" size="icon" data-grip className="-ml-2 size-11 shrink-0 touch-none cursor-grab sm:size-9"
            aria-label={`Move ${name}`} aria-describedby="drag-help" onKeyDown={drag.onKey}
          >
            <GripVertical aria-hidden />
          </Button>
        )}
        {renaming ? (
          <form className="flex flex-1 flex-wrap items-center gap-2" onSubmit={(e) => void rename(e)}>
            <Input aria-label={`New name for ${name}`} value={draft} maxLength={60} onChange={(e) => setDraft(e.target.value)} className="min-h-11 min-w-40 flex-1" autoFocus />
            <Button type="submit" size="sm" className="min-h-11 sm:min-h-9">Save</Button>
            <Button type="button" size="sm" variant="outline" className="min-h-11 sm:min-h-9" onClick={() => { setDraft(name); setRenaming(false); }}>Cancel</Button>
          </form>
        ) : (
          <CardTitle className="min-w-0 flex-1 break-words text-base font-medium">{name}</CardTitle>
        )}
        {!renaming && <Badge variant="secondary" className="shrink-0 tabular-nums">{count === 1 ? "1 device" : `${count} devices`}</Badge>}
      </CardHeader>
      <CardContent className="space-y-3">
        {inside.length > 0
          ? <ul aria-label={`Devices in ${name}`} className="flex flex-wrap gap-1.5">{inside.map((d) => <li key={d.id}><Badge variant="outline" className="font-normal">{d.name}</Badge></li>)}</ul>
          : <p className="text-sm text-muted-foreground">No devices yet.</p>}
        {manage && (
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" className="min-h-11 sm:min-h-9" onClick={() => setChoosing(true)}>Choose devices</Button>
            <Button size="sm" variant="outline" className="min-h-11 sm:min-h-9" onClick={() => { setDraft(name); setRenaming(true); }}><Pencil aria-hidden />Rename</Button>
            <Button size="sm" variant="outline" className="min-h-11 text-destructive sm:min-h-9" onClick={() => void remove()}><Trash2 aria-hidden />Delete</Button>
          </div>
        )}
      </CardContent>
      {choosing && <ChooseDevices room={name} devices={devices} rooms={rooms} onClose={() => setChoosing(false)} onChanged={onChanged} />}
    </Card>
  );
}

/** Rooms in the order the Devices page shows them, what's in each, and, for administrators, a way to change all of it. */
export function RoomsPage({ devices, rooms, onChanged }: Props) {
  const { can } = useAuth();
  const manage = can("extensions.manage");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const { items: names, said, drag } = useDragOrder(Object.keys(rooms), async (room, position) => {
    try {
      await api.editRoom(room, { position });
      onChanged();
    } catch (e) {
      fail("Couldn't move the room")(e);
    }
  });
  const placed = new Set(Object.values(rooms).flat());
  const loose = devices?.filter((d) => !placed.has(d.id)) ?? [];

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    try {
      await api.addRoom(name.trim());
      setName("");
      onChanged();
    } catch (err) {
      fail("Couldn't add the room")(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Rooms"
        description={manage ? "The rooms the Devices page groups by, in this order. A device is in one room at a time." : "The rooms the Devices page groups by. An administrator can change them."}
      />
      {manage && (
        <form className="flex flex-wrap items-center gap-2" onSubmit={(e) => void add(e)}>
          <Input aria-label="New room's name" placeholder="New room, like Kitchen" value={name} maxLength={60} onChange={(e) => setName(e.target.value)} className="min-h-11 min-w-48 flex-1 sm:max-w-sm" />
          <Button type="submit" className="min-h-11" disabled={busy || !name.trim()}><Plus aria-hidden />Add room</Button>
        </form>
      )}
      {manage && <p id="drag-help" className="sr-only">Drag a room to move it, or press the arrow keys on its handle.</p>}
      <p aria-live="polite" className="sr-only">{said}</p>
      {devices === null ? (
        <div className="grid gap-4 sm:grid-cols-2"><Skeleton className="h-40 rounded-xl" /><Skeleton className="h-40 rounded-xl" /></div>
      ) : names.length === 0 ? (
        <EmptyState icon={<DoorOpen />} title="No rooms yet">{manage ? "Add one above, then choose its devices." : "An administrator can add them."}</EmptyState>
      ) : (
        <div className="grid items-start gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {names.map((room) => (
            <RoomCard
              key={room} name={room} count={rooms[room]?.length ?? 0} devices={devices} rooms={rooms} manage={manage} onChanged={onChanged}
              drag={drag(room)}
            />
          ))}
        </div>
      )}
      {loose.length > 0 && (
        <section aria-labelledby="loose" className="space-y-2">
          <h2 id="loose" className="text-lg font-semibold">Not in a room</h2>
          <ul className="flex flex-wrap gap-1.5">{loose.map((d) => <li key={d.id}><Badge variant="outline" className="font-normal">{d.name}</Badge></li>)}</ul>
        </section>
      )}
    </div>
  );
}
