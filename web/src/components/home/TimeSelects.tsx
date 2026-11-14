import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export const pad2 = (n: number) => String(n).padStart(2, "0");
const HOURS = Array.from({ length: 24 }, (_, h) => pad2(h));

interface Props {
  /** Names the two selects: "<label>, hour" and "<label>, minute". */
  label: string;
  value: unknown;
  onChange: (value: string) => void;
  /** Minutes between the minute choices, 1 by default. */
  step?: number;
  /** The hour select's id, for a label or for focusing it. */
  id?: string;
  invalid?: boolean;
  describedBy?: string;
}

/** A time of day as two selects, hours and minutes in step, so it looks like every other field. */
export function TimeSelects({ label, value, onChange, step: given, id, invalid, describedBy }: Props) {
  const step = Math.max(1, Math.min(60, given ?? 1));
  const minutes = Array.from({ length: Math.ceil(60 / step) }, (_, i) => pad2(i * step));
  const [hour, minute] = typeof value === "string" && /^\d\d:\d\d$/.test(value) ? value.split(":") : ["", ""];
  // A minute between the steps, as a device may hold, still shows rather than reading as empty.
  const minuteOptions = minute && !minutes.includes(minute) ? [...minutes, minute].sort() : minutes;
  const set = (h: string, m: string) => onChange(`${h || "00"}:${m || "00"}`);
  const aria = { "aria-invalid": invalid || undefined, "aria-describedby": describedBy };
  return (
    <div className="flex items-center gap-1">
      <Select value={hour} onValueChange={(h) => set(h, minute)}>
        <SelectTrigger id={id} aria-label={`${label}, hour`} className="h-11 w-20" {...aria}><SelectValue placeholder="--" /></SelectTrigger>
        <SelectContent>{HOURS.map((h) => <SelectItem key={h} value={h}>{h}</SelectItem>)}</SelectContent>
      </Select>
      <span aria-hidden className="text-muted-foreground">:</span>
      <Select value={minute} onValueChange={(m) => set(hour, m)}>
        <SelectTrigger id={id && `${id}-minute`} aria-label={`${label}, minute`} className="h-11 w-20" {...aria}><SelectValue placeholder="--" /></SelectTrigger>
        <SelectContent>{minuteOptions.map((m) => <SelectItem key={m} value={m}>{m}</SelectItem>)}</SelectContent>
      </Select>
    </div>
  );
}
