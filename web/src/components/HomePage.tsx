import { useCallback, useState, type ReactNode } from "react";
import { Check, GripVertical, LayoutDashboard, Pencil, Plus, Puzzle, ScrollText, X } from "lucide-react";
import { toast } from "sonner";
import { api, type Device, type RenderedWidget, type Rooms, type Widgets } from "@/api";
import { EmptyState, PageHeader } from "@/components/PageHeader";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { WriteAccess, type Write } from "@/components/controls";
import { BUILTINS, Go, usePoll, WIDGETS_POLL_MS, type HomeData } from "@/components/home/builtins";
import { WidgetBlocks } from "@/components/home/WidgetBlocks";
import { useAuth } from "@/lib/auth";
import { useDragOrder, type Drag } from "@/lib/useDragOrder";
import { cn, messageOf } from "@/lib/utils";

export { problems, sortCleared, CLEARED_FOR_MS } from "@/components/home/builtins";

const STATUS_POLL_MS = 15_000; // extensions and automations change slowly; devices come live from the shell

interface FrameProps {
  title: string;
  icon?: ReactNode;
  from?: string;
  link?: ReactNode;
  wide?: boolean;
  editing: boolean;
  drag: Drag;
  onRemove: () => void;
  children: ReactNode;
}

/** One widget on Home. While Home is being arranged it can be dragged by its handle, or anywhere with a mouse, or removed. */
function Frame({ title, icon, from, link, wide, editing, drag, onRemove, children }: FrameProps) {
  return (
    <Card
      ref={drag.cardRef} onPointerDown={editing ? drag.onPointerDown : undefined} data-testid={`widget-${title}`}
      className={cn(wide && "lg:col-span-2", editing && "cursor-grab select-none", drag.dragging && "relative z-10 cursor-grabbing opacity-70 shadow-lg ring-2 ring-ring")}
    >
      <CardHeader className="flex flex-row items-center gap-2 space-y-0">
        {editing && (
          <Button type="button" variant="ghost" size="icon" data-grip className="-ml-2 size-11 shrink-0 touch-none cursor-grab sm:size-9" aria-label={`Move ${title}`} aria-describedby="widget-drag-help" onKeyDown={drag.onKey}>
            <GripVertical aria-hidden />
          </Button>
        )}
        <CardTitle className="flex min-w-0 flex-1 items-center gap-2 text-base font-medium">
          {icon && <span aria-hidden className="text-muted-foreground [&>svg]:size-4">{icon}</span>}
          <span className="min-w-0 break-words">{title}</span>
          {from && <span className="truncate text-xs font-normal text-muted-foreground">· {from}</span>}
        </CardTitle>
        {editing
          ? <Button type="button" variant="ghost" size="icon" className="size-11 shrink-0 sm:size-9" aria-label={`Remove ${title}`} onClick={onRemove}><X aria-hidden /></Button>
          : link}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

/** Every widget there is to place, grouped by where it comes from. Adding one puts it at the end of Home. */
function Picker({ layout, catalogue, onAdd, onClose }: { layout: string[]; catalogue: RenderedWidget[]; onAdd: (key: string) => void; onClose: () => void }) {
  const { can } = useAuth();
  // A device's own widget lives on that device's page.
  const offered = catalogue.filter((w) => !w.device);
  const scripts = offered.filter((w) => w.key.startsWith("script:"));
  const extensions = offered.filter((w) => w.key.startsWith("ext:"));
  const item = (key: string, title: string, detail: string) => {
    const added = layout.includes(key);
    return (
      <li key={key} className="flex min-h-11 items-center gap-3 py-2">
        <span className="min-w-0 flex-1 text-sm">
          <span className="block font-medium">{title}</span>
          <span className="block text-xs text-muted-foreground">{detail}</span>
        </span>
        <Button size="sm" variant={added ? "ghost" : "outline"} className="min-h-11 shrink-0 sm:min-h-9" disabled={added || layout.length >= 30} onClick={() => onAdd(key)} aria-label={added ? `${title} is on Home` : `Add ${title}`}>
          {added ? <><Check aria-hidden />Added</> : <><Plus aria-hidden />Add</>}
        </Button>
      </li>
    );
  };
  const group = (title: string, icon: ReactNode, children: ReactNode, empty: ReactNode) => (
    <section className="space-y-1">
      <h3 className="flex items-center gap-2 text-sm font-semibold"><span aria-hidden className="text-muted-foreground [&>svg]:size-4">{icon}</span>{title}</h3>
      {children ?? <p className="py-2 text-sm text-muted-foreground">{empty}</p>}
    </section>
  );
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add widgets</DialogTitle>
          <DialogDescription>Home shows them in the order you add them. Drag them around afterwards.</DialogDescription>
        </DialogHeader>
        <div className="space-y-5">
          {group("Built in", <LayoutDashboard />, (
            <ul className="divide-y divide-border/60">{BUILTINS.filter((b) => !b.needs || can(b.needs)).map((b) => item(b.key, b.title, b.description))}</ul>
          ), null)}
          {group("From your automations", <ScrollText />, scripts.length ? (
            <ul className="divide-y divide-border/60">{scripts.map((w) => item(w.key, w.title, `From ${w.from}${w.practising ? ", practising" : ""}`))}</ul>
          ) : null, <>None yet. An automation offers one with <code className="font-mono text-xs">home.widget(…)</code>{can("automations.edit") && <>; see the <Go page="automations">Automations</Go> page</>}.</>)}
          {group("From integrations and plugins", <Puzzle />, extensions.length ? (
            <ul className="divide-y divide-border/60">{extensions.map((w) => item(w.key, w.title, `From ${w.from}`))}</ul>
          ) : null, "None of yours offers one yet. Extensions can, from the marketplace or your own.")}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** The first page, made of the widgets the home chose: built-in ones, and ones from automations and extensions. */
export function HomePage({ devices, rooms, write }: { devices: Device[] | null; rooms: Rooms; write: Write }) {
  const { can } = useAuth();
  const arrange = can("devices.write");
  const [editing, setEditing] = useState(false);
  const [picking, setPicking] = useState(false);
  const loadWidgets = useCallback(() => api.widgets(), []);
  const [widgets, setWidgets] = usePoll<Widgets>(loadWidgets, WIDGETS_POLL_MS);
  const layout = widgets?.layout ?? [];
  // Only asked for while a widget on Home shows them.
  const wantsExtensions = can("extensions.read") && layout.includes("builtin/needs-you");
  const wantsAutomations = can("automations.read") && (layout.includes("builtin/needs-you") || layout.includes("builtin/automations"));
  const loadExtensions = useCallback(() => api.extensions(), []);
  const loadAutomations = useCallback(() => api.automations(), []);
  const [extensions] = usePoll(wantsExtensions ? loadExtensions : null, STATUS_POLL_MS);
  const [automations] = usePoll(wantsAutomations ? loadAutomations : null, STATUS_POLL_MS);

  const save = async (next: string[]) => {
    const before = layout;
    const show = (order: string[]) => setWidgets((w) => w && { ...w, layout: order });
    show(next);
    try {
      await api.setDashboard(next);
      show(next); // a poll that left before the save may have brought the old order back
    } catch (e) {
      show(before);
      toast.error("Home wasn't saved", { description: messageOf(e, "Try again.") });
    }
  };
  const refresh = () => { void loadWidgets().then(setWidgets, () => {}); };
  const titleOf = (key: string) => BUILTINS.find((b) => b.key === key)?.title ?? widgets?.widgets.find((w) => w.key === key)?.title ?? "Widget";
  const { items, said, drag } = useDragOrder(layout, (_key, _position, list) => save(list), titleOf);

  if (devices === null || widgets === null) return <div className="space-y-4"><PageHeader title="Home" /><Skeleton className="h-40 rounded-xl" /><Skeleton className="h-40 rounded-xl" /></div>;

  const data: HomeData = { devices, rooms, extensions, automations };
  const addButton = <Button className="min-h-11" onClick={() => setPicking(true)}><Plus aria-hidden />Add widgets</Button>;
  const empty = layout.length === 0;

  return (
    <WriteAccess.Provider value={arrange}>
      <div className="space-y-6">
        <PageHeader
          title="Home"
          description={empty ? undefined : "How everything is right now. It keeps itself up to date."}
          actions={arrange && !empty && (editing
            ? <>{addButton}<Button variant="outline" className="min-h-11" onClick={() => setEditing(false)}><Check aria-hidden />Done</Button></>
            : <Button variant="outline" className="min-h-11" onClick={() => setEditing(true)}><Pencil aria-hidden />Edit Home</Button>)}
        />
        <p id="widget-drag-help" className="sr-only">Drag a widget to move it, or press the arrow keys on its handle.</p>
        <p aria-live="polite" className="sr-only">{said}</p>

        {empty ? (
          <EmptyState icon={<LayoutDashboard />} title="Make Home yours" action={arrange ? addButton : undefined}>
            {arrange
              ? "Add the widgets you want to see first: what needs you, your devices and heating, the weather, or buttons from your own automations."
              : "Nothing is on Home yet. Someone with the Family role or higher can add widgets."}
          </EmptyState>
        ) : (
          <div className="grid items-start gap-4 lg:grid-cols-2">
            {items.map((key) => {
              const frame = { editing, drag: drag(key), onRemove: () => void save(layout.filter((k) => k !== key)) };
              const builtin = BUILTINS.find((b) => b.key === key);
              if (builtin) {
                return (
                  <Frame key={key} {...frame} title={builtin.title} icon={builtin.icon} wide={builtin.wide} link={builtin.link && <Go page={builtin.link.page}>{builtin.link.text}</Go>}>
                    {builtin.needs && !can(builtin.needs) ? <p className="text-sm text-muted-foreground">Your role can't see this.</p> : builtin.Body(data)}
                  </Frame>
                );
              }
              const widget = widgets.widgets.find((w) => w.key === key);
              if (!widget) {
                return (
                  <Frame key={key} {...frame} title="Missing widget">
                    <p className="text-sm text-muted-foreground">The automation or extension it came from is gone or stopped. It shows again when that's back{arrange ? ", or remove it while editing Home" : ""}.</p>
                  </Frame>
                );
              }
              return (
                <Frame key={key} {...frame} title={widget.title} from={widget.from}>
                  <WidgetBlocks widget={widget} devices={devices} write={write} onPressed={arrange ? refresh : undefined} />
                </Frame>
              );
            })}
          </div>
        )}
        {picking && <Picker layout={layout} catalogue={widgets.widgets} onAdd={(key) => { void save([...layout, key]); setEditing(true); }} onClose={() => setPicking(false)} />}
      </div>
    </WriteAccess.Provider>
  );
}
