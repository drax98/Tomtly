import { Fragment, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { api, type DataPoint, type Device, type RenderedWidget, type Value, type WidgetBlock, type WidgetTone } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Control, type Write } from "@/components/controls";
import { findDp, label as codeLabel } from "@/lib/device";
import { cn, messageOf } from "@/lib/utils";
import { ErrorText } from "@/components/PageHeader";

const TONES: Record<WidgetTone, string> = { good: "text-good", warn: "text-warn", bad: "text-destructive" };
const tone = (t?: WidgetTone) => (t ? TONES[t] : undefined);

const shown = (v: Value | null | undefined) =>
  v === null || v === undefined ? "–" : typeof v === "boolean" ? (v ? "Yes" : "No") : typeof v === "number" ? v.toLocaleString(undefined, { maximumFractionDigits: 2 }) : v;

const CONTROLS = new Set(["number", "toggle", "select", "button"]);

/** A device block's min and max, given in the units shown, narrow the device's own range: never past it. */
function narrowed(dp: DataPoint, min?: number, max?: number): DataPoint {
  if (min === undefined && max === undefined) return dp;
  const raw = (v: number) => Math.round(v * 10 ** (dp.scale ?? 0));
  const lo = dp.values.min, hi = dp.values.max;
  const clamp = (v: number) => Math.min(hi ?? v, Math.max(lo ?? v, v));
  return { ...dp, values: { ...dp.values, min: min === undefined ? lo : clamp(raw(min)), max: max === undefined ? hi : clamp(raw(max)) } };
}

/** Consecutive stats share a row, and so do consecutive inputs and buttons, the way a form reads. */
function groups(blocks: WidgetBlock[]) {
  const out: { kind: string; blocks: WidgetBlock[] }[] = [];
  for (const b of blocks) {
    const kind = b.type === "stat" ? "stat" : CONTROLS.has(b.type) ? "control" : b.type;
    const last = out.at(-1);
    if (last && last.kind === kind && (kind === "stat" || kind === "control")) last.blocks.push(b);
    else out.push({ kind, blocks: [b] });
  }
  return out;
}

/** A group block starts a titled section that runs to the next group; blocks before the first group have no title. */
function sections(blocks: WidgetBlock[]) {
  const out: { title?: string; blocks: WidgetBlock[] }[] = [{ blocks: [] }];
  for (const b of blocks) {
    if (b.type === "group") out.push({ title: b.label, blocks: [] });
    else out[out.length - 1].blocks.push(b);
  }
  return out.filter((s, i) => i > 0 || s.blocks.length > 0);
}

interface Props {
  widget: RenderedWidget;
  devices: Device[];
  write: Write;
  /** Missing for people who can't change things: they see the widget without its inputs and buttons. */
  onPressed?: () => void;
}

/** What a script or an extension says its widget shows. It's data, drawn here in the page's own style. */
export function WidgetBlocks({ widget, devices, write, onPressed }: Props) {
  const [inputs, setInputs] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const blocks = widget.blocks ?? [];
  const valueOf = (b: WidgetBlock) => (b.name! in inputs ? inputs[b.name!] : b.type === "select" ? (b.value ?? b.options?.[0]) : b.type === "toggle" ? (b.value ?? false) : (b.value ?? ""));
  const set = (name: string, value: unknown) => setInputs((now) => ({ ...now, [name]: value }));

  const press = async (b: WidgetBlock) => {
    const input: Record<string, unknown> = {};
    for (const i of blocks) if (i.name && i.type !== "button") input[i.name] = valueOf(i) === "" ? null : valueOf(i);
    setBusy(b.action!);
    try {
      const { live } = await api.pressWidget(widget.key, b.action!, { ...input, ...b.args });
      if (live) toast.success(`${b.label}: done`);
      else toast.info(`${widget.from} is practising`, { description: "It only logged what it would do. Switch it live on the Automations page to make it act." });
      onPressed?.();
    } catch (e) {
      toast.error(`${b.label} didn't work`, { description: messageOf(e, "Try again.") });
    } finally {
      setBusy(null);
    }
  };

  const control = (b: WidgetBlock, i: number): ReactNode => {
    const id = `${widget.key}-${b.name ?? i}`;
    switch (b.type) {
      case "number":
        return (
          <div key={i} className="space-y-1.5">
            <Label htmlFor={id}>{b.label ?? b.name}{b.unit && <span className="text-muted-foreground"> ({b.unit})</span>}</Label>
            <Input id={id} type="number" inputMode="decimal" min={b.min} max={b.max} step={b.step ?? "any"} className="min-h-11 w-32"
              value={String(valueOf(b) ?? "")} onChange={(e) => set(b.name!, e.target.value === "" ? "" : Number(e.target.value))} />
          </div>
        );
      case "toggle":
        return (
          <div key={i} className="flex min-h-11 items-center gap-2">
            <Switch id={id} checked={valueOf(b) === true} onCheckedChange={(on) => set(b.name!, on)} />
            <Label htmlFor={id}>{b.label ?? b.name}</Label>
          </div>
        );
      case "select":
        return (
          <div key={i} className="space-y-1.5">
            <Label htmlFor={id}>{b.label ?? b.name}</Label>
            <Select value={String(valueOf(b) ?? "")} onValueChange={(v) => set(b.name!, v)}>
              <SelectTrigger id={id} className="h-11 min-w-36"><SelectValue /></SelectTrigger>
              <SelectContent>{b.options?.map((o) => <SelectItem key={o} value={o}>{o}</SelectItem>)}</SelectContent>
            </Select>
          </div>
        );
      default:
        return (
          <Button key={i} className={cn("min-h-11", b.tone === "bad" && "bg-destructive text-destructive-foreground hover:bg-destructive/90")} disabled={busy !== null} onClick={() => void press(b)}>
            {busy === b.action ? "Working…" : b.label}
          </Button>
        );
    }
  };

  if (widget.error) return <ErrorText>This widget failed to draw: {widget.error}</ErrorText>;
  if (!widget.blocks) return <p className="text-sm text-muted-foreground">Drawing it…</p>;
  if (blocks.length === 0) return <p className="text-sm text-muted-foreground">Nothing to show right now.</p>;

  const draw = (list: WidgetBlock[]) => (
    <div className="space-y-4">
      {groups(list).map((g, gi) => {
        if (g.kind === "stat") {
          return (
            <dl key={gi} className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              {g.blocks.map((b, i) => (
                <div key={i}>
                  <dt className="text-xs text-muted-foreground">{b.label}</dt>
                  <dd className={cn("text-2xl font-semibold tabular-nums", tone(b.tone))}>{shown(b.value)}{b.unit && <span className="ml-1 text-base font-normal text-muted-foreground">{b.unit}</span>}</dd>
                </div>
              ))}
            </dl>
          );
        }
        if (g.kind === "control") {
          if (!onPressed) return null;
          return <div key={gi} className="flex flex-wrap items-end gap-3">{g.blocks.map(control)}</div>;
        }
        const b = g.blocks[0];
        if (b.type === "text") return <p key={gi} className={cn("whitespace-pre-line break-words text-sm", tone(b.tone))}>{b.text}</p>;
        if (b.type === "rows") {
          return (
            <dl key={gi} className="divide-y divide-border/60 text-sm">
              {b.rows?.map((r, i) => (
                <div key={i} className="flex items-baseline justify-between gap-3 py-1.5">
                  <dt className="min-w-0 break-words text-muted-foreground">{r.label}</dt>
                  <dd className={cn("shrink-0 text-right tabular-nums", tone(r.tone))}>{shown(r.value)}</dd>
                </div>
              ))}
            </dl>
          );
        }
        const device = devices.find((d) => d.id === b.device);
        const dp = device && findDp(device, b.code!);
        if (!device || !dp) return <p key={gi} className="text-sm text-muted-foreground">{b.label ?? b.device}: not found</p>;
        return (
          <div key={gi} className="flex min-h-11 flex-wrap items-center justify-between gap-3 text-sm">
            <span className="min-w-0 break-words">{b.label ?? `${device.name} · ${codeLabel(dp.code)}`}</span>
            <Control device={device} dp={narrowed(dp, b.min, b.max)} write={write} />
          </div>
        );
      })}
    </div>
  );

  return (
    <div className="space-y-4">
      {sections(blocks).map((s, si) =>
        s.title === undefined ? (
          <Fragment key={si}>{draw(s.blocks)}</Fragment>
        ) : (
          <section key={si} aria-label={s.title} className="space-y-3 rounded-lg border border-border/60 p-3">
            <h3 className="text-sm font-medium">{s.title}</h3>
            {s.blocks.length > 0 && draw(s.blocks)}
          </section>
        ),
      )}
    </div>
  );
}
