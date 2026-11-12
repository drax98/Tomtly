import {
  ArrowDown, ArrowUp, DoorClosed, DoorOpen, Flame, House, MapPin, Minus, Pause, Play, Plus, Square, Target, Wind,
} from "lucide-react";
import type { DataPoint, Device } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { FollowingSlider } from "@/components/ui/slider";
import { capitalize, display, findDp, isAdvanced, label } from "@/lib/device";
import { cn } from "@/lib/utils";
import { ActionButton, Control, DeviceCard, EnumSelect, PowerSwitch, Row, useCanWrite, usePinned, type CardProps } from "./controls";

function Battery({ device }: { device: Device }) {
  const dp = findDp(device, "battery", "battery_percentage");
  if (typeof dp?.value !== "number") return null;
  const low = dp.value <= 20;
  return (
    <div className="space-y-1.5">
      <div className="flex justify-between text-xs text-muted-foreground">
        <span>Battery</span>
        <span className={cn("tabular-nums", low && "font-medium text-destructive")}>{low ? "Low, " : ""}{dp.value}%</span>
      </div>
      <Progress value={Math.min(dp.value, 100)} className="h-2" aria-label="Battery level" />
    </div>
  );
}

/** What the person pinned, after what the card shows on its own. Everything else a device reports is in its drawer. */
function PinnedValues({ device, write }: CardProps) {
  const pinned = usePinned().map((id) => device.dps?.find((d) => d.dp === id)).filter((d): d is DataPoint => d !== undefined);
  if (pinned.length === 0) return null;
  return (
    <div className="divide-y divide-border/60" data-testid="pinned">
      {pinned.map((d) => <Row key={d.dp} label={label(d.code)}><Control device={device} dp={d} write={write} /></Row>)}
    </div>
  );
}

const worthShowing = (d: DataPoint) => d.code !== "unavailable" && (d.value !== null || d.writable || d.type === "Action");

const isOpenValue = (value: unknown) => value === true || (typeof value === "string" && /^open/i.test(value));

const climateParts = (device: Device) => {
  const mode = findDp(device, "mode");
  return {
    power: findDp(device, "switch", "power"), current: findDp(device, "temp_current"), target: findDp(device, "temp_set"),
    heating: findDp(device, "heating_state"), mode: mode?.writable && mode.type === "Enum" ? mode : undefined,
  };
};

export function ClimateCard({ device, write }: CardProps) {
  const canWrite = useCanWrite();
  const { power, current, target, heating, mode } = climateParts(device);
  const window = findDp(device, "window_state", "window_check", "open_window");
  const step = target?.values.step ?? 1;
  const nudge = (direction: number) => {
    if (!target || typeof target.value !== "number") return;
    const next = target.value + direction * step;
    const { min = -Infinity, max = Infinity } = target.values;
    if (next >= min && next <= max) write(device.id, target.dp, next);
  };
  const isHeating = heating?.value === true;
  const off = power?.value === false;
  const windowOpen = isOpenValue(window?.value);
  const state = off ? "Off" : isHeating ? (target ? `Heating to ${display(target)}` : "Heating") : target ? `Holding ${display(target)}` : "Idle";
  return (
    <DeviceCard device={device} tone={windowOpen ? "warn" : isHeating && !off ? "heat" : undefined} action={power && <PowerSwitch device={device} dp={power} write={write} />}>
      <div className="space-y-1.5">
        <div className="text-reading font-semibold tabular-nums">{current ? display(current) : "–"}</div>
        <p className={cn("flex items-center gap-1.5 text-sm", isHeating && !off ? "font-medium text-heat" : "text-muted-foreground")}>
          {isHeating && !off && <Flame className="size-4" aria-hidden />}
          {state}
        </p>
      </div>
      {windowOpen && (
        <p className="flex items-center gap-2 rounded-lg bg-warn/15 px-3 py-2 text-sm font-medium text-warn"><Wind className="size-4" aria-hidden />Window open</p>
      )}
      {target && (
        <Row label="Aim for">
          <div className="flex items-center gap-1">
            <Button variant="outline" size="icon" className="size-11 rounded-full" disabled={!canWrite || off} aria-label={`Lower ${device.name}`} onClick={() => nudge(-1)}><Minus /></Button>
            <span className="w-20 text-center text-lg font-medium tabular-nums">{display(target)}</span>
            <Button variant="outline" size="icon" className="size-11 rounded-full" disabled={!canWrite || off} aria-label={`Raise ${device.name}`} onClick={() => nudge(1)}><Plus /></Button>
          </div>
        </Row>
      )}
      {target && off && canWrite && <p className="text-right text-xs text-muted-foreground">Turn it on to change the target.</p>}
      {mode && <Row label="Mode"><EnumSelect device={device} dp={mode} write={write} /></Row>}
      <PinnedValues device={device} write={write} />
    </DeviceCard>
  );
}

const ACTION_ICONS: Record<string, typeof Play> = { start: Play, pause: Pause, stop: Square, dock: House, spot: Target, find: MapPin };
const ACTION_TEXT: Record<string, string> = { start: "Start cleaning", pause: "Pause", stop: "Stop", dock: "Go to dock", spot: "Clean a spot", find: "Find it" };

const vacuumParts = (device: Device) => ({ state: findDp(device, "state"), command: findDp(device, "command"), speed: findDp(device, "fan_speed") });

export function VacuumCard({ device, write }: CardProps) {
  const { state, command, speed } = vacuumParts(device);
  const error = findDp(device, "error_code");
  const cleaning = typeof state?.value === "string" && /clean|returning|docking|going/.test(state.value);
  const failed = typeof error?.value === "number" && error.value !== 0;
  return (
    <DeviceCard
      device={device}
      action={state && (
        <Badge variant="secondary" className={cn(cleaning && "bg-good/15 text-good", failed && "bg-destructive/15 text-destructive")}>
          {failed ? `Error ${error?.value}` : display(state)}
        </Badge>
      )}
    >
      <Battery device={device} />
      {command && (
        <div className="grid grid-cols-2 gap-2">
          {command.values.range?.map((action) => {
            const Icon = ACTION_ICONS[action];
            return (
              <ActionButton
                key={action}
                text={ACTION_TEXT[action] ?? capitalize(action)}
                icon={Icon && <Icon aria-hidden />}
                variant={action === "start" ? "default" : "outline"}
                className="min-h-11"
                onSend={() => write(device.id, command.dp, action, false)}
              />
            );
          })}
        </div>
      )}
      {speed && <Row label="Suction power"><EnumSelect device={device} dp={speed} write={write} /></Row>}
      <PinnedValues device={device} write={write} />
    </DeviceCard>
  );
}

export function SensorCard({ device, write }: CardProps) {
  const open = findDp(device, "open");
  const isOpen = open?.value === true;
  const known = open?.value !== null && open?.value !== undefined;
  const Icon = isOpen ? DoorOpen : DoorClosed;
  return (
    <DeviceCard
      device={device}
      tone={isOpen ? "warn" : undefined}
      action={open && device.online && (
        <Badge variant="secondary" className={cn("px-3 py-1 text-sm", isOpen && "bg-warn/20 text-warn")}>
          <Icon className="size-4" aria-hidden />{!known ? "Unknown" : isOpen ? "Open" : "Closed"}
        </Badge>
      )}
    >
      <Battery device={device} />
      <PinnedValues device={device} write={write} />
    </DeviceCard>
  );
}

const coverParts = (device: Device) => ({ control: findDp(device, "control"), position: findDp(device, "percent_control"), reported: findDp(device, "percent_state") });

export function CoverCard({ device, write }: CardProps) {
  const { control, position, reported } = coverParts(device);
  const known = typeof reported?.value === "number" ? reported.value : typeof position?.value === "number" ? position.value : null;
  const value = known ?? 0;
  const canWrite = useCanWrite();
  // Curtain motors say open or on for the same thing, and a value outside the device's range would be refused.
  const send = (...names: string[]) => control && write(device.id, control.dp, control.values.range?.find((r) => names.includes(r)) ?? names[0], false);
  return (
    <DeviceCard device={device} action={known !== null && <Badge variant="secondary" className="px-3 py-1 text-sm tabular-nums">{value === 0 ? "Closed" : value === 100 ? "Open" : `${value}% open`}</Badge>}>
      {position && (
        <FollowingSlider
          aria-label={`${device.name} position`}
          value={value}
          min={0}
          max={100}
          step={position.values.step ?? 1}
          disabled={!canWrite}
          onCommit={(v) => write(device.id, position.dp, v)}
        />
      )}
      {control && (
        <div className="grid grid-cols-3 gap-2">
          <ActionButton variant="outline" className="min-h-11" text="Open" icon={<ArrowUp aria-hidden />} onSend={() => send("open", "on")} />
          <ActionButton variant="outline" className="min-h-11" text="Stop" icon={<Square aria-hidden />} onSend={() => send("stop")} />
          <ActionButton variant="outline" className="min-h-11" text="Close" icon={<ArrowDown aria-hidden />} onSend={() => send("close", "off")} />
        </div>
      )}
      <Battery device={device} />
      <PinnedValues device={device} write={write} />
    </DeviceCard>
  );
}

const PRIMARY_POWER = /^(power|switch|switch_led|switch_1)$/;
const MAIN_ROWS = 8; // a device described by its spec can have dozens of values; what can be changed comes first

/** Works for any device from any adapter: a main switch, what it reports, what can be changed, the rest under More. */
function genericParts(device: Device, pinned: string[]) {
  const dps = (device.dps ?? []).filter(worthShowing);
  const channels = dps.some((d) => d.code === "switch_2"); // then switch_1 is one of several, not the device's power
  const power = channels ? undefined : dps.find((d) => d.type === "Boolean" && d.writable && PRIMARY_POWER.test(d.code));
  // What's pinned shows in its own place on the card, so it doesn't take a main row.
  const rest = dps.filter((d) => d !== power && !/^battery/.test(d.code) && !pinned.includes(d.dp));
  const plain = rest.filter((d) => !isAdvanced(d));
  const main = plain.length <= MAIN_ROWS ? plain : [...plain.filter((d) => d.writable), ...plain.filter((d) => !d.writable)].slice(0, MAIN_ROWS);
  return { channels, power, rest, main };
}

export function GenericCard({ device, write }: CardProps) {
  const { channels, power, main } = genericParts(device, usePinned());
  const on = power?.value === true;
  return (
    <DeviceCard device={device} tone={on ? "on" : undefined} action={power && <PowerSwitch device={device} dp={power} write={write} />}>
      <Battery device={device} />
      {main.length > 0 && <div className="divide-y divide-border/60">{main.map((d) => <Row key={d.dp} label={channels && d.code === "switch_1" ? "Switch 1" : label(d.code)}><Control device={device} dp={d} write={write} /></Row>)}</div>}
      <PinnedValues device={device} write={write} />
    </DeviceCard>
  );
}

/** The values a device's card always shows on its own, so pinning them would show them twice. Its battery counts too. */
export function shownOnCard(device: Device): DataPoint[] {
  const card = cardFor(device);
  const battery = findDp(device, "battery", "battery_percentage");
  const parts: (DataPoint | undefined)[] =
    card === ClimateCard ? Object.values(climateParts(device))
    : card === VacuumCard ? [...Object.values(vacuumParts(device)), battery]
    : card === CoverCard ? [...Object.values(coverParts(device)), battery]
    : card === SensorCard ? [findDp(device, "open"), battery]
    : (() => { const g = genericParts(device, []); return [g.power, ...g.main, battery]; })();
  return parts.filter((d): d is DataPoint => d !== undefined);
}

/** A card for what a device is, not who made it, so an adapter gets these by setting "kind". */
export function cardFor(device: Device) {
  if (device.kind === "vacuum" && findDp(device, "state", "command")) return VacuumCard;
  if (device.kind === "cover" && findDp(device, "control", "percent_control", "percent_state")) return CoverCard;
  if (device.kind === "climate" && findDp(device, "temp_current", "temp_set")) return ClimateCard;
  if (device.kind === "sensor" && findDp(device, "open")) return SensorCard;
  return GenericCard;
}

