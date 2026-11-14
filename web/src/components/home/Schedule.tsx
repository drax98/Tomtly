import type { WidgetBlock } from "@/api";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import { TimeSelects } from "./TimeSelects";

/** One period of a day's program as the form holds it: "" for a time not set. */
export interface Row {
  time: string;
  temp?: number;
}

export const startRows = (b: WidgetBlock): Row[] => (b.periods ?? []).map((p) => ({ time: p.time ?? "", temp: p.temp }));

const blank = (r: Row) => !r.time && r.temp === undefined;

/** What Save sends for a schedule: every row, or null when all are empty, which keeps what the device has. */
export const sentRows = (rows: Row[]) => (rows.every(blank) ? null : rows.map((r) => ({ time: r.time || null, temp: r.temp ?? null })));

export interface Problem {
  row: number;
  text: string;
  /** A half-set row: shown only once someone tries to save. */
  onSave: boolean;
}

/** What's wrong with a program, one problem a row: a start that isn't after the row before's, or a half-set row. */
export function scheduleProblems(rows: Row[]): Problem[] {
  if (rows.every(blank)) return [];
  const out: Problem[] = [];
  rows.forEach((r, i) => {
    const before = rows[i - 1]?.time;
    if (r.time && before && r.time <= before) out.push({ row: i, text: `Must start after ${before}`, onSave: false });
    else if (!r.time || r.temp === undefined) out.push({ row: i, text: "Set a time and temperature", onSave: true });
  });
  return out;
}

const shownProblems = (rows: Row[], tried: boolean) => scheduleProblems(rows).filter((p) => tried || !p.onSave);

/** Adjacent schedules draw as one set of tabs, so a run of them is one part of a form. */
export function withRuns(blocks: WidgetBlock[]): (WidgetBlock | WidgetBlock[])[] {
  const out: (WidgetBlock | WidgetBlock[])[] = [];
  for (const b of blocks) {
    const last = out.at(-1);
    if (b.type !== "schedule") out.push(b);
    else if (Array.isArray(last)) last.push(b);
    else out.push([b]);
  }
  return out;
}

/** The first problem in an edited schedule, which blocks Save: its run's tab to show and the control to focus. */
export function blocking(form: WidgetBlock[], rowsOf: (b: WidgetBlock) => Row[], edited: (name: string) => boolean, prefix: string) {
  const runs = withRuns(form).filter((p) => Array.isArray(p));
  for (const b of runs.flat()) {
    const rows = rowsOf(b);
    const p = edited(b.name!) ? scheduleProblems(rows)[0] : undefined;
    if (!p) continue;
    const cell = `${prefix}-${b.name}-${p.row}`;
    return { run: runs.find((r) => r.includes(b))![0].name!, name: b.name!, focus: p.onSave && rows[p.row].time ? `${cell}-temp` : cell };
  }
}

interface Props {
  run: WidgetBlock[];
  /** Starts every control's id, so Save can focus one. */
  prefix: string;
  rowsOf: (b: WidgetBlock) => Row[];
  onChange: (name: string, rows: Row[]) => void;
  /** Someone tried to save, so half-set rows say so. */
  tried: boolean;
  tab?: string;
  onTab: (name: string) => void;
}

/** A run of day programs: tabs labelled by day type when there are several, opening on the one running now. */
export function Schedules({ run, tab, onTab, ...rest }: Props) {
  if (run.length === 1) {
    return (
      <div className="w-full space-y-2">
        <h4 className="text-sm font-medium">{run[0].label}</h4>
        <Program b={run[0]} {...rest} />
      </div>
    );
  }
  const active = run.find((b) => b.name === tab)?.name ?? run.find((b) => b.now !== undefined)?.name ?? run[0].name!;
  const count = (b: WidgetBlock) => (b.name === active ? 0 : shownProblems(rest.rowsOf(b), rest.tried).length);
  return (
    <Tabs value={active} onValueChange={onTab} className="w-full">
      <TabsList className="h-auto w-full flex-wrap">
        {run.map((b) => (
          <TabsTrigger key={b.name} value={b.name!} className="min-h-9 flex-1">
            {b.label}{count(b) > 0 && ` (${count(b)} problem${count(b) > 1 ? "s" : ""})`}
          </TabsTrigger>
        ))}
      </TabsList>
      {run.map((b) => <TabsContent key={b.name} value={b.name!}><Program b={b} {...rest} /></TabsContent>)}
    </Tabs>
  );
}

/** Whole degrees from min to max, plus any the program holds outside them so they still show. */
function temperatures(b: WidgetBlock, rows: Row[]) {
  const lo = Math.ceil(b.min ?? 5), hi = Math.floor(b.max ?? 30);
  const range = Array.from({ length: Math.max(0, hi - lo + 1) }, (_, i) => lo + i);
  const held = rows.flatMap((r) => (r.temp === undefined ? [] : [r.temp]));
  return [...new Set([...range, ...held])].sort((x, y) => x - y);
}

function Program({ b, prefix, rowsOf, onChange, tried }: Omit<Props, "run" | "tab" | "onTab"> & { b: WidgetBlock }) {
  const rows = rowsOf(b);
  const problems = shownProblems(rows, tried);
  const unit = b.unit ?? "°C";
  const temps = temperatures(b, rows);
  const set = (i: number, change: Partial<Row>) => onChange(b.name!, rows.map((r, n) => (n === i ? { ...r, ...change } : r)));
  return (
    <div className="space-y-2">
      <DayBar rows={rows} now={b.now} />
      <div aria-hidden className="flex justify-between px-2 text-xs text-muted-foreground"><span>From</span><span>Temp ({unit})</span></div>
      {rows.map((r, i) => {
        const id = `${prefix}-${b.name}-${i}`;
        const problem = problems.find((p) => p.row === i);
        const aria = { "aria-invalid": problem ? true : undefined, "aria-describedby": problem && `${id}-error` };
        const until = rows[(i + 1) % rows.length].time;
        const now = b.now === i;
        return (
          <div key={i}>
            <div role="group" aria-label={`Period ${i + 1} of ${rows.length}`} className={cn("flex flex-wrap items-center gap-2 rounded-md py-1 pr-1 pl-2", now && "border-l-2 border-primary bg-primary/10")}>
              <TimeSelects id={id} label={`Period ${i + 1} start`} step={b.step} value={r.time} onChange={(time) => set(i, { time })} invalid={!!problem} describedBy={aria["aria-describedby"]} />
              {until && <span className="hidden text-sm text-muted-foreground sm:inline">until {until}</span>}
              {now && <span className="text-xs font-medium text-primary"><span aria-hidden>now</span><span className="sr-only">running now</span></span>}
              <Select value={r.temp === undefined ? "" : String(r.temp)} onValueChange={(t) => set(i, { temp: Number(t) })}>
                <SelectTrigger id={`${id}-temp`} aria-label={`Period ${i + 1} temperature, ${unit}`} className="ml-auto h-11 w-20" {...aria}><SelectValue placeholder="--" /></SelectTrigger>
                <SelectContent>{temps.map((t) => <SelectItem key={t} value={String(t)}>{t}°</SelectItem>)}</SelectContent>
              </Select>
            </div>
            {problem && <p id={`${id}-error`} role="alert" className="px-2 pt-1 text-sm text-destructive">{problem.text}</p>}
          </div>
        );
      })}
    </div>
  );
}

const minutes = (t: string) => Number(t.slice(0, 2)) * 60 + Number(t.slice(3));

/** The day's pieces: each period to the next start, and midnight to the first start still running the last period. */
function segments(rows: Row[]) {
  const starts = rows.map((r) => minutes(r.time));
  const out = rows.map((r, i) => ({ period: i, temp: r.temp!, minutes: (starts[i + 1] ?? 24 * 60) - starts[i] }));
  if (starts[0] > 0) out.unshift({ period: rows.length - 1, temp: rows.at(-1)!.temp!, minutes: starts[0] });
  return out;
}

/** The program over 24 hours, warmer periods in warmer colours. Drawn only for a complete program in order. */
function DayBar({ rows, now }: { rows: Row[]; now?: number }) {
  const empty = rows.every(blank);
  if (empty || scheduleProblems(rows).length > 0) {
    return (
      <div aria-hidden={!empty} className="flex min-h-8 items-center justify-center rounded-md border border-dashed px-2 py-1 text-center text-xs text-muted-foreground">
        {empty && "Not read yet. Fill every row to set it, or leave all empty to keep it."}
      </div>
    );
  }
  const temps = rows.map((r) => r.temp!);
  const lo = Math.min(...temps), spread = Math.max(...temps) - lo;
  const heat = (t: number) => (spread ? Math.round(((t - lo) / spread) * 100) : 50);
  return (
    <div aria-hidden>
      <div data-testid="day-bar" className="flex h-8 gap-px overflow-hidden rounded-md">
        {segments(rows).map((s, i) => (
          <div key={i} className={cn("flex min-w-0 basis-0 items-center justify-center text-xs font-medium text-background", s.period === now && "ring-2 ring-inset ring-foreground")}
            style={{ flexGrow: s.minutes, background: `color-mix(in oklch, var(--heat) ${heat(s.temp)}%, var(--cool))` }}>
            {s.minutes >= 120 && `${s.temp}°`}
          </div>
        ))}
      </div>
      <div className="mt-1 flex justify-between text-xs text-muted-foreground">{[0, 6, 12, 18, 24].map((h) => <span key={h}>{h}</span>)}</div>
    </div>
  );
}
