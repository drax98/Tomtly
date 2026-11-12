import { createContext, useContext, useEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode } from "react";
import { Blinds, Bot, Check, Fan, Lightbulb, Lock, Plug, ToggleRight, WashingMachine, Router, ShieldAlert, Thermometer, WifiOff, Wind, DoorClosed, CircleDot } from "lucide-react";
import type { DataPoint, Device, Value } from "@/api";
import { Button, type ButtonProps } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Slider, useFollowingValue } from "@/components/ui/slider";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { capitalize, display, findDp, hueToHex, label } from "@/lib/device";
import { cn } from "@/lib/utils";

export type Write = (id: string, dp: string, value: Value, track?: boolean) => void | Promise<void>;

export interface CardProps {
  device: Device;
  write: Write;
}

/** The values pinned to the card being drawn, by dp, in order. */
export const Pinned = createContext<string[]>([]);
export const usePinned = () => useContext(Pinned);

/** Opens a device's drawer. Without it, as in tests of one card, a card is only a card. */
export const OpenDevice = createContext<((id: string) => void) | null>(null);

/** Whether this person may change devices. Defaults to yes; the devices page turns it off for viewers. */
export const WriteAccess = createContext(true);
export const useCanWrite = () => useContext(WriteAccess);

export const READ_ONLY_REASON = "Viewers can look but not change. Ask an administrator for the Family role.";

const KIND_ICONS: Record<string, typeof Plug> = {
  light: Lightbulb, plug: Plug, fan: Fan, climate: Thermometer, air: Wind, cover: Blinds, vacuum: Bot, sensor: DoorClosed, safety: ShieldAlert, infrastructure: Router, switch: ToggleRight, lock: Lock, appliance: WashingMachine,
};

export type Tone = "on" | "heat" | "warn" | undefined;

const TONES: Record<Exclude<Tone, undefined>, string> = {
  on: "border-primary/35 bg-primary/[0.07]",
  heat: "border-heat/40 bg-heat/[0.08]",
  warn: "border-warn/50 bg-warn/[0.09]",
};

/** Anything on a card that isn't one of its controls opens the drawer. */
const opensDrawer = (open: ((id: string) => void) | null, id: string) => (e: MouseEvent<HTMLElement>) => {
  // A menu's options are drawn outside the card, but React still hands their clicks to it.
  if (!open || !e.currentTarget.contains(e.target as Node)) return;
  if ((e.target as HTMLElement).closest("a, button, input, label, [role=slider], [role=switch], [role=combobox]") || window.getSelection()?.toString()) return;
  open(id);
};

export function DeviceCard({ device, action, tone, children }: { device: Device; action?: ReactNode; tone?: Tone; children?: ReactNode }) {
  const canWrite = useCanWrite();
  const open = useContext(OpenDevice);
  const Icon = KIND_ICONS[device.kind] ?? CircleDot;
  const note = findDp(device, "unavailable")?.value; // an integration saying why, when power and Wi-Fi aren't the problem
  const card = (
    <Card
      tabIndex={canWrite ? undefined : 0}
      className={cn("transition-colors", open && "cursor-pointer hover:border-ring/50", tone && device.online && TONES[tone], !device.online && "bg-muted/50")}
      onClick={opensDrawer(open, device.id)}
    >
      <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0">
        <div className="flex min-w-0 items-center gap-3">
          <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-lg bg-secondary text-muted-foreground", tone === "on" && "bg-primary/15 text-primary", tone === "heat" && "bg-heat/15 text-heat", tone === "warn" && "bg-warn/15 text-warn")}>
            <Icon className="size-5" aria-hidden />
          </span>
          <div className="min-w-0 space-y-1">
            <CardTitle className="truncate text-base font-medium leading-snug" title={device.name}>
              {open
                ? <button type="button" className="max-w-full truncate text-left underline-offset-4 hover:underline" aria-label={`Details of ${device.name}`} onClick={() => open(device.id)}>{device.name}</button>
                : device.name}
            </CardTitle>
            {!device.online && (
              <p className="flex items-center gap-1 text-xs text-offline"><WifiOff className="size-3.5 shrink-0" aria-hidden />{typeof note === "string" ? note : "Offline. Check it has power and Wi-Fi."}</p>
            )}
          </div>
        </div>
        {action}
      </CardHeader>
      {children && <CardContent className={cn("space-y-3", !device.online && "opacity-60")}>{children}</CardContent>}
    </Card>
  );
  if (canWrite) return card;
  // A tooltip is only an extra: the controls are visibly disabled, and this says why.
  return (
    <Tooltip>
      <TooltipTrigger asChild>{card}</TooltipTrigger>
      <TooltipContent>{READ_ONLY_REASON}</TooltipContent>
    </Tooltip>
  );
}

export function Row({ label: text, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-h-11 items-center justify-between gap-3 py-2 text-sm">
      <span className="text-muted-foreground">{text}</span>
      {children}
    </div>
  );
}

/** A button that says "Sent" for a moment, so a press never feels like nothing happened. */
export function ActionButton({ text, icon, onSend, ...props }: { text: string; icon?: ReactNode; onSend: () => void } & Omit<ButtonProps, "onClick">) {
  const canWrite = useCanWrite();
  const [sent, setSent] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <Button
      {...props}
      aria-label={text}
      disabled={props.disabled || !canWrite}
      onClick={() => {
        onSend();
        setSent(true);
        clearTimeout(timer.current);
        timer.current = setTimeout(() => setSent(false), 1500);
      }}
    >
      {sent ? <Check aria-hidden /> : icon}
      {sent ? "Sent" : text}
    </Button>
  );
}

export function PowerSwitch({ device, dp, write }: { device: Device; dp: DataPoint; write: Write }) {
  const canWrite = useCanWrite();
  return (
    <Switch
      aria-label={`${device.name} ${label(dp.code)}`}
      checked={dp.value === true}
      disabled={!dp.writable || !canWrite}
      onCheckedChange={(on) => write(device.id, dp.dp, on)}
    />
  );
}

/** The right control for any data point, so devices from any adapter work without a dedicated card. */
export function Control({ device, dp, write }: { device: Device; dp: DataPoint; write: Write }) {
  if (dp.type === "Action") {
    return (
      <div className="flex flex-wrap justify-end gap-2">
        {dp.values.range?.map((action) => (
          <ActionButton key={action} text={capitalize(action)} variant="outline" size="sm" className="min-h-11" onSend={() => write(device.id, dp.dp, action, false)} />
        ))}
      </div>
    );
  }
  if (!dp.writable) return <span className="min-w-0 break-words text-right font-medium tabular-nums" title={typeof dp.value === "string" ? dp.value : undefined}>{display(dp)}</span>;
  if (dp.type === "Boolean") return <PowerSwitch device={device} dp={dp} write={write} />;
  if (dp.type === "Enum") return <EnumSelect device={device} dp={dp} write={write} />;
  if (dp.type === "Integer" && /hue$/.test(dp.code) && dp.values.max === 360) return <ColourPicker device={device} dp={dp} write={write} />;
  if (dp.type === "Integer" && dp.values.min !== undefined && dp.values.max !== undefined) {
    return <NumberSlider device={device} dp={dp} write={write} />;
  }
  return <span className="font-medium tabular-nums">{display(dp)}</span>;
}

export function EnumSelect({ device, dp, write }: { device: Device; dp: DataPoint; write: Write }) {
  const canWrite = useCanWrite();
  return (
    <Select value={typeof dp.value === "string" ? dp.value : undefined} disabled={!canWrite} onValueChange={(v) => write(device.id, dp.dp, v)}>
      <SelectTrigger className="h-11 w-40" aria-label={`${device.name} ${label(dp.code)}`}>
        <SelectValue placeholder="–" />
      </SelectTrigger>
      <SelectContent>
        {dp.values.range?.map((v) => <SelectItem key={v} value={v}>{capitalize(v)}</SelectItem>)}
      </SelectContent>
    </Select>
  );
}

/** A light's hue and saturation on a wheel: around it for hue, out from the middle for saturation, as the light takes them. */
export function ColourPicker({ device, dp, write }: { device: Device; dp: DataPoint; write: Write }) {
  const canWrite = useCanWrite();
  const saturation = findDp(device, dp.code.replace(/hue$/, "saturation"));
  const satMax = saturation?.values.max ?? 1;
  const [picked, setPicked] = useState<Hs | null>(null);
  const shown = picked ?? {
    hue: typeof dp.value === "number" ? dp.value : 0,
    sat: typeof saturation?.value === "number" ? saturation.value / satMax : 1,
  };
  const colour = hueToHex(shown.hue, shown.sat);
  const name = `${device.name} colour`;

  const send = async (next: Hs) => {
    // One after the other: each part is written onto the colour the last one left.
    await write(device.id, dp.dp, next.hue);
    if (saturation?.writable) await write(device.id, saturation.dp, Math.round(next.sat * satMax));
    setPicked(null);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = WHEEL_KEYS[e.key];
    if (!step) return;
    e.preventDefault();
    const next = { hue: (shown.hue + step[0] + 360) % 360, sat: Math.min(1, Math.max(0, shown.sat + step[1])) };
    setPicked(next);
    void send(next);
  };

  return (
    <Dialog onOpenChange={() => setPicked(null)}>
      <DialogTrigger asChild>
        <button
          type="button"
          disabled={!canWrite}
          aria-label={name}
          className="h-11 w-16 rounded-md border border-input focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
          style={{ background: colour }}
        />
      </DialogTrigger>
      <DialogContent className="max-w-xs">
        <DialogHeader>
          <DialogTitle>{name}</DialogTitle>
          <DialogDescription>Around the wheel for the colour, toward the middle for paler. Arrow keys work too.</DialogDescription>
        </DialogHeader>
        <div
          role="slider"
          tabIndex={0}
          aria-label={name}
          aria-valuemin={0}
          aria-valuemax={359}
          aria-valuenow={shown.hue}
          aria-valuetext={`Hue ${shown.hue}, saturation ${Math.round(shown.sat * 100)}%`}
          className="relative mx-auto aspect-square w-full max-w-64 cursor-crosshair touch-none rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          // Each stop blends linearly, which is exactly hue and saturation at full brightness.
          style={{ background: "radial-gradient(closest-side, #fff, transparent), conic-gradient(from 90deg, red, yellow, lime, cyan, blue, magenta, red)" }}
          onPointerDown={(e) => {
            e.currentTarget.setPointerCapture(e.pointerId);
            setPicked(wheelAt(e));
          }}
          onPointerMove={(e) => {
            if (e.currentTarget.hasPointerCapture(e.pointerId)) setPicked(wheelAt(e));
          }}
          onPointerUp={(e) => void send(wheelAt(e))}
          onKeyDown={onKeyDown}
        >
          <span
            aria-hidden
            className="pointer-events-none absolute size-6 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-white shadow-md"
            style={{
              left: `${50 + Math.cos((shown.hue * Math.PI) / 180) * shown.sat * 50}%`,
              top: `${50 + Math.sin((shown.hue * Math.PI) / 180) * shown.sat * 50}%`,
              background: colour,
            }}
          />
        </div>
      </DialogContent>
    </Dialog>
  );
}

type Hs = { hue: number; sat: number };

const WHEEL_KEYS: Record<string, [number, number]> = { ArrowRight: [5, 0], ArrowLeft: [-5, 0], ArrowUp: [0, 0.05], ArrowDown: [0, -0.05] };

/** The hue and saturation under the pointer: its angle clockwise from the right, and its distance out of the radius. */
function wheelAt(e: PointerEvent<HTMLDivElement>): Hs {
  const box = e.currentTarget.getBoundingClientRect();
  const x = e.clientX - box.left - box.width / 2;
  const y = e.clientY - box.top - box.height / 2;
  return { hue: Math.round((Math.atan2(y, x) * 180) / Math.PI + 360) % 360, sat: Math.min(1, Math.hypot(x, y) / (box.width / 2)) };
}

export function NumberSlider({ device, dp, write }: { device: Device; dp: DataPoint; write: Write }) {
  const canWrite = useCanWrite();
  const value = typeof dp.value === "number" ? dp.value : dp.values.min!;
  // The number beside it moves with the thumb at once; the device gets the value in the background.
  const { shown, sliderProps } = useFollowingValue(value, (v) => write(device.id, dp.dp, v));
  return (
    <div className="flex w-48 items-center gap-3">
      <Slider
        aria-label={`${device.name} ${label(dp.code)}`}
        min={dp.values.min}
        max={dp.values.max}
        step={dp.values.step ?? 1}
        disabled={!canWrite}
        {...sliderProps}
      />
      <span className="w-16 whitespace-nowrap text-right text-sm font-medium tabular-nums">{display({ ...dp, value: shown })}</span>
    </div>
  );
}
