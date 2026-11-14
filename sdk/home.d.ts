// The API every automation script can use. Scripts are plain synchronous TypeScript: there is no network,
// file access, setTimeout or async. Every *.ts file in your automations folder is one script; put shared helpers
// in a subfolder and import them by relative path. Only .ts and .js files in that folder can be imported.
//
// Reading or changing anything needs the owner's approval, per script and per thing. A call that isn't approved is
// refused and recorded, and acts as if the value isn't known yet: it never throws, and the rest of the script keeps running.

type Value = boolean | number | string;

interface Device {
  readonly id: string;
  readonly name: string;
  /** False when the service can't reach the device right now, or reading it isn't approved. */
  online(): boolean;
  /** How long it has been unreachable, 0 while online or if reading it isn't approved. Short gaps are normal when a gateway reconnects. */
  offlineSeconds(): number;
  /**
   * The latest known value, by code as listed in GET /api/devices.
   * Undefined until the device has reported it, which battery sensors may take a while to do after a restart,
   * or if reading it isn't approved.
   */
  get(code: string): Value | undefined;
  /**
   * Asks the device to change. Does nothing if it already has that value, is unreachable right now or changing it
   * isn't approved; retried if it doesn't take effect.
   */
  set(code: string, value: Value): void;
}

/** A device whose codes and values the editor knows. R lists what can be read, and W what can be written. */
interface TypedDevice<R, W> {
  readonly id: string;
  readonly name: string;
  /** False when the service can't reach the device right now, or reading it isn't approved. */
  online(): boolean;
  /** How long it has been unreachable, 0 while online or if reading it isn't approved. Short gaps are normal when a gateway reconnects. */
  offlineSeconds(): number;
  /** The latest known value. Undefined until the device has reported it, or if reading it isn't approved. */
  get<C extends keyof R>(code: C): R[C] | undefined;
  /** Asks the device to change. Does nothing if it already has that value, is unreachable right now or changing it isn't approved. */
  set<C extends keyof W>(code: C, value: W[C]): void;
}

/** Your devices by id, filled in for you from the real devices when the editor opens. */
interface DeviceMap {}

/** Each plugin's data by id, filled in from its latest data, or by the extension's types.d.ts. */
interface PluginMap {}

/** What each plugin can be asked to do, filled in from its actions. */
interface PluginActionMap {}

interface Plugin<T, A extends string = string> {
  readonly id: string;
  /** The latest data. Undefined until the first successful fetch or if reading it isn't approved, so check it before you use it. */
  data(): T | undefined;
  /** How old the data is, undefined before the first fetch or if reading it isn't approved. A failing plugin keeps its last data, and this keeps growing. */
  ageSeconds(): number | undefined;
  /**
   * Asks the plugin to do something, like send a notification, without waiting for it. In shadow mode this only logs what
   * it would do. An identical call within 30 seconds is dropped, so calling it from reconcile is safe, not a flood.
   * Does nothing if this action isn't approved. An action's name can't hold a colon.
   */
  action(name: A, args?: unknown): void;
}

/** What a script lists with home.needs. Device ids under read and write, plugin ids under data and as keys of actions. */
interface Needs {
  read?: string[];
  write?: string[];
  data?: string[];
  actions?: Record<string, string[]>;
}

type Tone = "good" | "warn" | "bad";

/**
 * One piece of a widget on Home. A widget is data: the page draws it in its own style. Inputs (number, toggle, select)
 * hold what someone types or picks, and a button sends all of them, with its args, to the action it names.
 */
type WidgetBlock =
  /** Starts a group titled label: the blocks after it, up to the next group, are drawn together under that title. */
  | { type: "group"; label: string }
  | { type: "text"; text: string; tone?: Tone }
  | { type: "stat"; label: string; value?: Value | null; unit?: string; tone?: Tone }
  | { type: "rows"; rows: { label: string; value?: Value | null; tone?: Tone }[] }
  /** A device's value with its control, like on the Devices page, by device id and value code. min and max narrow a
   * slider to the range that matters, in the units shown (°C, not tenths), never past the device's own limits. */
  | { type: "device"; device: string; code: string; label?: string; min?: number; max?: number }
  | { type: "number"; name: string; label?: string; value?: number; min?: number; max?: number; step?: number; unit?: string }
  | { type: "toggle"; name: string; label?: string; value?: boolean }
  | { type: "select"; name: string; label?: string; value?: string; options: string[] }
  /** A time of day as "HH:MM", in steps of step minutes (1 by default). */
  | { type: "time"; name: string; label?: string; value?: string; step?: number }
  | { type: "button"; label: string; action: string; args?: Record<string, unknown>; tone?: Tone }
  /**
   * A segmented control of 2 to 4 options. Picking one calls action at once with { [name]: option, ...args }, and
   * nothing else: not the widget's inputs, nor a dialog's form, which stays open. The pick shows busy until render
   * gives it as value, for up to 10 seconds.
   */
  | { type: "choice"; name: string; label: string; value?: string; options: string[]; action: string; args?: Record<string, unknown> }
  /**
   * A day's program, only in a dialog: up to 12 periods, each a start "HH:MM" and a whole temperature from min to max,
   * either left out while unknown. now is the index of the period running now. Save sends [{ time, temp }, ...] for
   * every period, or null when all are empty. Adjacent schedules show as tabs by label, opening on the one with now.
   * Periods out of order are shown and must be fixed before Save, but only once someone edits that schedule.
   */
  | { type: "schedule"; name: string; label: string; periods: { time?: string; temp?: number }[]; now?: number; min?: number; max?: number; step?: number; unit?: string }
  /**
   * A button labelled label that opens a form: title heads it, text explains it, and blocks are its fields (group, text,
   * rows, number, toggle, select, time, schedule and choice, up to 40). Save calls action with the form's values by
   * name, plus args, the way a button does; Cancel sends nothing. A form without inputs has only Close. Each opening
   * starts from the values render gives, and edits outlast a redraw while it's open.
   */
  | { type: "dialog"; label: string; action: string; title?: string; text?: string; args?: Record<string, unknown>; blocks: WidgetBlock[] };

interface WidgetDefinition {
  /** Up to 60 characters, shown on the widget and in the list of widgets to add. */
  title: string;
  /** Returns what the widget shows now, up to 40 blocks. Drawn again every 5 seconds and after a button is pressed. */
  render(): WidgetBlock[];
  /**
   * What the buttons do, by the name a button's action gives. input holds every input's value by name, and the button's
   * args. It runs like the rest of the script: a practising script only logs what it would do, and anything it reads or
   * changes needs approving. A button sets nothing a second time within 30 seconds if the value is the same.
   */
  actions?: Record<string, (input: Record<string, any>) => void>;
}

interface Home {
  /**
   * Offers a widget for Home, by a key that's unique in this script: letters, digits, - and _. Up to 10 per script.
   * Someone places it on Home from the list of widgets.
   */
  widget(key: string, definition: WidgetDefinition): void;
  /** Finds a device by id. Names aren't unique, so ids are the only safe reference. An id that isn't approved always gets a device, whose name is its id. */
  device<K extends keyof DeviceMap>(id: K): DeviceMap[K];
  device(id: string): Device;
  /** Finds a plugin from the plugins list in config.json, or an extension's id, such as home.plugin<Weather>("weather"). */
  plugin<K extends keyof PluginMap>(id: K): Plugin<PluginMap[K], K extends keyof PluginActionMap ? PluginActionMap[K] : never>;
  plugin<T = any>(id: string): Plugin<T>;
  /**
   * Runs fn about once a second. Describe what should be true right now, rather than reacting to events,
   * so the house recovers by itself after a restart or a lost command.
   */
  reconcile(fn: () => void): void;
  /** Runs fn once a day at a local time such as "08:30", in HOME_TIMEZONE. */
  daily(at: string, fn: () => void): void;
  /** Adds a line to the activity log on the Automations page, up to 120 lines a minute. */
  log(message: string): void;
  /**
   * Lists access ahead of time, so the owner can approve it before a rarely used branch needs it. It grants nothing, and
   * what a script uses without listing it is still asked for when it runs.
   */
  needs(list: Needs): void;
}

declare const home: Home;
