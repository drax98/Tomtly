import { useState, type FormEvent } from "react";
import { Copy, Pencil, Pin, WifiOff } from "lucide-react";
import { toast } from "sonner";
import { api, type Device } from "@/api";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { shownOnCard } from "@/components/cards";
import { Control, type Write } from "@/components/controls";
import { capitalize, findDp, label } from "@/lib/device";
import { messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

export const MAX_PINS = 12; // the service refuses more

interface Props {
  device: Device;
  room?: string;
  pinned: string[];
  /** Missing for people who can't change devices: they see the pins, but can't move them. */
  onPins?: (dps: string[]) => void;
  write: Write;
  onClose: () => void;
  /** Every room there is, to move it to. */
  rooms?: string[];
  /** Missing for people who can't rename or move devices. */
  onEdited?: () => void;
}

function Fact({ name, children }: { name: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-9 items-baseline justify-between gap-3 py-1.5 text-sm">
      <dt className="shrink-0 text-muted-foreground">{name}</dt>
      <dd className="min-w-0 break-words text-right">{children}</dd>
    </div>
  );
}

/** Everything about one device: what it is, where it is, and every value it has, each of which can be pinned to its card. */
const NEW_ROOM = "\u0000new"; // neither can be a room's name
const NO_ROOM = "\u0000none";

/** The name and room, as the owner wants them. */
function EditDevice({ device, room, rooms, onDone }: { device: Device; room?: string; rooms: string[]; onDone: (saved: boolean) => void }) {
  const [name, setName] = useState(device.name);
  const [choice, setChoice] = useState(room ?? NO_ROOM);
  const [newRoom, setNewRoom] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const original = device.originalName ?? device.name;
  const save = async (e: FormEvent) => {
    e.preventDefault();
    const nextRoom = choice === NO_ROOM ? "" : choice === NEW_ROOM ? newRoom.trim() : choice;
    if (choice === NEW_ROOM && !nextRoom) return setError("Give the new room a name, or pick one.");
    setBusy(true);
    setError(null);
    try {
      // The integration's own name is stored as none, so a rename there later still shows.
      await api.editDevice(device.id, { name: name.trim() === original ? "" : name.trim(), room: nextRoom });
      toast.success("Saved");
      onDone(true);
    } catch (err) {
      setError(messageOf(err, "That didn't work. Try again."));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="space-y-3" onSubmit={(e) => void save(e)}>
      <div className="space-y-1.5">
        <Label htmlFor="device-name">Name</Label>
        <Input id="device-name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} className="min-h-11" />
        <p className="text-xs text-muted-foreground">Leave it empty for the integration's name: {original}.</p>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="device-room">Room</Label>
        <Select value={choice} onValueChange={setChoice}>
          <SelectTrigger id="device-room" className="h-11 w-full"><SelectValue /></SelectTrigger>
          <SelectContent>
            {rooms.map((r) => <SelectItem key={r} value={r}>{r}</SelectItem>)}
            <SelectItem value={NO_ROOM}>No room</SelectItem>
            <SelectItem value={NEW_ROOM}>A new room…</SelectItem>
          </SelectContent>
        </Select>
        {choice === NEW_ROOM && <Input aria-label="New room's name" placeholder="Like Kitchen" value={newRoom} maxLength={60} onChange={(e) => setNewRoom(e.target.value)} className="min-h-11" autoFocus />}
      </div>
      {error && <ErrorText>{error}</ErrorText>}
      <div className="flex gap-2">
        <Button type="submit" className="min-h-11" disabled={busy}>{busy ? "Saving…" : "Save"}</Button>
        <Button type="button" variant="outline" className="min-h-11" onClick={() => onDone(false)}>Cancel</Button>
      </div>
    </form>
  );
}

export function DeviceDrawer({ device, room, pinned, onPins, write, onClose, rooms = [], onEdited }: Props) {
  const [editing, setEditing] = useState(false);
  const onCard = shownOnCard(device).map((d) => d.dp);
  const values = (device.dps ?? []).filter((d) => d.code !== "unavailable");
  const note = findDp(device, "unavailable")?.value;
  const copyId = async () => {
    try {
      await navigator.clipboard.writeText(device.id);
      toast.success("Device ID copied");
    } catch {
      toast.error("Couldn't copy it", { description: "Select the ID and copy it yourself." });
    }
  };
  const toggle = (dp: string) => {
    if (!onPins) return;
    if (pinned.includes(dp)) onPins(pinned.filter((p) => p !== dp));
    else if (pinned.length >= MAX_PINS) toast.error(`A card holds up to ${MAX_PINS} pinned values`, { description: "Unpin one first." });
    else onPins([...pinned, dp]);
  };

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-md">
        <SheetHeader className="pr-8 text-left">
          <SheetTitle className="break-words">{device.name}</SheetTitle>
          <SheetDescription className="flex flex-wrap items-center gap-2">
            <span>{capitalize(device.kind || "device")}{device.product ? ` · ${device.product}` : ""}</span>
            {device.online
              ? <Badge variant="secondary" className="bg-good/15 text-good">Online</Badge>
              : <Badge variant="secondary" className="text-offline"><WifiOff aria-hidden className="size-3" />Offline</Badge>}
          </SheetDescription>
        </SheetHeader>

        <div className="mt-4 space-y-6">
          {!device.online && <p className="text-sm text-offline">{typeof note === "string" ? note : "Offline. Check it has power and Wi-Fi."}</p>}
          {device.error && <p role="alert" className="break-words text-sm font-medium text-destructive">The last command failed: {device.error}</p>}

          <section aria-labelledby="device-facts" className="space-y-1">
            <div className="flex items-center justify-between gap-2">
              <h3 id="device-facts" className="text-sm font-semibold">About it</h3>
              {onEdited && !editing && <Button variant="outline" size="sm" className="min-h-11 sm:min-h-8" onClick={() => setEditing(true)}><Pencil aria-hidden />Edit</Button>}
            </div>
            {editing && onEdited && <EditDevice device={device} room={room} rooms={rooms} onDone={(saved) => { setEditing(false); if (saved) onEdited(); }} />}
            <dl className="divide-y divide-border/60">
              <Fact name="Device ID">
                <span className="inline-flex items-center gap-1">
                  <code className="break-all font-mono text-xs">{device.id}</code>
                  <Button variant="ghost" size="icon" className="size-9 shrink-0" aria-label="Copy the device ID" onClick={() => void copyId()}><Copy aria-hidden /></Button>
                </span>
              </Fact>
              {device.originalName && <Fact name="Integration's name">{device.originalName}</Fact>}
              {!editing && <Fact name="Room">{room ?? <span className="text-muted-foreground">Not in a room</span>}</Fact>}
              <Fact name="Integration">{device.adapter}</Fact>
              {device.product && <Fact name="Model">{device.product}</Fact>}
              {device.category && <Fact name="Category">{device.category}</Fact>}
            </dl>
            {!onEdited && <p className="text-xs text-muted-foreground">An administrator can rename it or move it to another room.</p>}
          </section>

          <section aria-labelledby="device-values" className="space-y-1">
            <h3 id="device-values" className="text-sm font-semibold">Everything it reports</h3>
            <p className="text-xs text-muted-foreground">
              {onPins ? "Pin a value to show it on the card." : "Pinned values show on the card."}
            </p>
            {values.length === 0 ? (
              <p className="py-2 text-sm text-muted-foreground">It hasn't reported anything yet.</p>
            ) : (
              <ul className="divide-y divide-border/60">
                {values.map((d) => {
                  const isPinned = pinned.includes(d.dp);
                  const always = onCard.includes(d.dp);
                  return (
                    <li key={d.dp} className="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 py-1.5 text-sm">
                      <span className="min-w-0 flex-1">
                        <span className="block">{label(d.code)}</span>
                        <span className="block font-mono text-xs text-muted-foreground">{d.code}</span>
                      </span>
                      <Control device={device} dp={d} write={write} />
                      {always ? (
                        // Always shown, so it reads as pinned and can't be unpinned.
                        <span role="img" aria-label="Always on the card" title="Always on the card" className="flex size-9 shrink-0 items-center justify-center text-muted-foreground">
                          <Pin aria-hidden className="size-4 fill-current" />
                        </span>
                      ) : onPins ? (
                        <Button
                          variant={isPinned ? "secondary" : "ghost"} size="icon" className="size-9 shrink-0"
                          aria-pressed={isPinned} aria-label={isPinned ? `Unpin ${label(d.code)} from the card` : `Pin ${label(d.code)} to the card`}
                          onClick={() => toggle(d.dp)}
                        >
                          <Pin aria-hidden className={isPinned ? "fill-current text-primary" : undefined} />
                        </Button>
                      ) : isPinned ? (
                        <Pin aria-label="Pinned" className="size-4 shrink-0 text-muted-foreground" />
                      ) : (
                        <span className="w-9" />
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        </div>
      </SheetContent>
    </Sheet>
  );
}
