import type { DataPoint, Device, Rooms, Value } from "@/api";

export function findDp(device: Device, ...codes: string[]): DataPoint | undefined {
  for (const code of codes) {
    const dp = device.dps?.find((d) => d.code === code);
    if (dp) return dp;
  }
  return undefined;
}

// Plain names for the codes devices report, so nobody has to know what "temp_set" means.
const LABELS: Record<string, string> = {
  switch: "Power",
  switch_1: "Power",
  power: "Power",
  switch_led: "Light",
  pm25: "Air quality (PM2.5)",
  temp_set: "Target temperature",
  temp_current: "Temperature",
  heating_state: "Heating",
  cur_power: "Power use now",
  cur_voltage: "Voltage",
  cur_current: "Current",
  add_ele: "Energy used",
  anion: "Ionizer",
  percent_control: "Position",
  percent_state: "Position now",
  battery_percentage: "Battery",
  humidity_value: "Humidity",
  bright_value: "Brightness",
  child_lock: "Child lock",
  fan_speed: "Fan speed",
  window_state: "Window",
  open: "Open or closed",
  c_f: "Temperature unit",
  relay_status: "After a power cut",
  co_status: "CO alarm",
  co_value: "CO level",
  smoke_sensor_status: "Smoke",
  temper_alarm: "Tamper alarm", // Tuya's spelling of tamper
  muffling: "Alarm silenced",
  swing_vertical: "Airflow up and down",
  swing_horizontal: "Airflow side to side",
  x_fan: "Dry after shutdown",
  frost_protection: "Frost protection (8 °C)",
  command: "Actions",
  self_clean_minutes: "Self-clean time",
  colour_data: "Colour",
  colour_data_v2: "Colour",
  colour_hue: "Colour hue",
  colour_saturation: "Colour saturation",
  colour_brightness: "Colour brightness",
  work_mode: "Light mode",
  temp_value: "White warmth",
  temp_value_v2: "White warmth",
  bright_value_v2: "Brightness",
  humidity_current: "Humidity",
  humidity_set: "Target humidity",
  electricity_left: "Battery",
  doorcontact_state: "Open or closed",
  pir: "Motion",
  presence_state: "Presence",
  watersensor_state: "Water leak",
  gas_sensor_status: "Gas",
  cur_power1: "Power use now",
};

export function label(code: string): string {
  const known = LABELS[code];
  if (known) return known;
  const words = code.replace(/_/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function capitalize(text: string): string {
  const words = text.replace(/_/g, " "); // device names like power_off and check_success
  return words.charAt(0).toUpperCase() + words.slice(1);
}

// "true" means nothing to a person; each kind of yes/no gets the words it deserves.
function boolWords(code: string): [string, string] {
  if (/^(open|doorcontact_state|window)/.test(code)) return ["Open", "Closed"];
  if (code === "heating_state") return ["Heating", "Idle"];
  if (/^(smoke|gas|water|pir|alarm|presence|co_|co2|ch4|ch2o|sos|shock|vibration)/.test(code)) return ["Detected", "Clear"];
  if (/^(temper_alarm|no_water|tank_removed|bottom_door_open|pump_running|map_present|muffling)$/.test(code)) return ["Yes", "No"];
  return ["On", "Off"];
}

/** Formats a value for reading, applying the data point's scale and unit. */
export function display(dp: DataPoint, value: Value | null | undefined = dp.value): string {
  if (value === null || value === undefined) return "–";
  if (typeof value === "boolean") return boolWords(dp.code)[value ? 0 : 1];
  if (typeof value === "number") {
    const scaled = dp.scale ? (value / 10 ** dp.scale).toFixed(dp.scale) : String(value);
    return dp.values.unit ? `${scaled} ${dp.values.unit}` : scaled;
  }
  return dp.type === "String" ? displayText(value) : capitalize(value);
}

const MAX_TEXT = 48;

/** Text a device sent: JSON as "key: value" pairs, and a long blob, like a Raw value's base64, cut short. */
export function displayText(text: string, max = MAX_TEXT): string {
  let shown = text;
  try {
    const parsed: unknown = JSON.parse(text);
    if (parsed !== null && typeof parsed === "object") {
      shown = Object.entries(parsed).map(([k, v]) => `${capitalize(k)}: ${typeof v === "object" ? JSON.stringify(v) : v}`).join(", ");
    }
  } catch {
    // not JSON
  }
  return shown.length > max ? `${shown.slice(0, max)}…` : shown;
}

// Values a person rarely wants on a card. They stay reachable under "More" and through the API.
const ADVANCED = /^(countdown|relay_status|light_mode|c_f|child_lock|control_back_mode|border|switch_inching|colour_data|error_code|fault|sound|display|light$|filter_days_left|work_mode|temp_value|scene_data|music_data|control_data|rhythm_mode|sleep_mode|wakeup_mode|power_memory|do_not_disturb|cycle_timing|random_timing|switch_backlight|overcharge_switch|inching|command|motor_|favorite_speed|manual_level|temperature_unit|purified_volume|average_aqi|aqi_state|bottom_door|filter_hours|self_clean|actual_speed|pump_|flex_switch|swing_|fresh_air|x_fan|health|sleep|frost_|energy_|total_clean|clean_|map_present|main_brush|side_brush|sensor_dirty|sound_volume)/;

export function isAdvanced(dp: DataPoint): boolean {
  return ADVANCED.test(dp.code);
}

export interface Group {
  title: string;
  devices: Device[];
}

const KINDS: [string, string][] = [
  ["climate", "Climate"],
  ["air", "Air"],
  ["fan", "Fans"],
  ["light", "Lights"],
  ["plug", "Plugs"],
  ["switch", "Switches"],
  ["cover", "Blinds"],
  ["vacuum", "Cleaning"],
  ["sensor", "Sensors"],
  ["safety", "Safety"],
  ["lock", "Locks"],
  ["appliance", "Appliances"],
  ["infrastructure", "Infrastructure"],
];

/** Groups by room in the configured order, with anything unassigned last. */
export function groupByRoom(devices: Device[], rooms: Rooms): Group[] {
  const assigned = new Set(Object.values(rooms).flat());
  const groups = Object.entries(rooms).map(([title, ids]) => ({
    title,
    devices: devices.filter((d) => ids.includes(d.id)),
  }));
  groups.push({ title: "Other", devices: devices.filter((d) => !assigned.has(d.id)) });
  return groups.filter((g) => g.devices.length > 0);
}

/** Groups by what a device is. An adapter's own kind still gets a group, after the known ones. */
export function groupByKind(devices: Device[]): Group[] {
  const known = new Map(KINDS);
  const groups: Group[] = KINDS.map(([kind, title]) => ({ title, devices: devices.filter((d) => d.kind === kind) }));
  const others = [...new Set(devices.map((d) => d.kind).filter((k) => !known.has(k)))].sort();
  for (const kind of others) {
    groups.push({ title: kind === "other" ? "Other" : capitalize(kind), devices: devices.filter((d) => d.kind === kind) });
  }
  return groups.filter((g) => g.devices.length > 0);
}

const POWER = /^(power|switch|switch_led|switch_1)$/;

/** A one-line read of a room or group: what's on, how warm, what's open or offline. */
export function summarize(devices: Device[]): string {
  const parts: string[] = [];
  const switchable = devices.filter((d) => d.online && findDp(d, "switch", "power", "switch_1", "switch_led"));
  const on = switchable.filter((d) => d.dps?.some((x) => POWER.test(x.code) && x.value === true)).length;
  if (switchable.length) parts.push(`${on} of ${switchable.length} on`);
  const temps = devices.flatMap((d) => {
    const dp = d.online ? findDp(d, "temp_current") : undefined;
    return dp && typeof dp.value === "number" ? [dp.value / 10 ** dp.scale] : [];
  });
  if (temps.length) parts.push(`${(temps.reduce((a, b) => a + b, 0) / temps.length).toFixed(1)}°C`);
  const open = devices.filter((d) => findDp(d, "open")?.value === true).length;
  if (open) parts.push(`${open} open`);
  const offline = devices.filter((d) => !d.online && !findDp(d, "unavailable")).length; // one that can't be reached here isn't news
  if (offline) parts.push(`${offline} offline`);
  return parts.join(" · ");
}

/** Matches a device by name, room, type or any word typed, ignoring case. */
export function matches(device: Device, room: string, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const haystack = `${device.name} ${room} ${device.kind} ${KINDS.find(([k]) => k === device.kind)?.[1] ?? ""}`.toLowerCase();
  return words.every((w) => haystack.includes(w));
}

/** A hue (0 to 360) and saturation (0 to 1) as #rrggbb at full brightness, as a light shows it. */
export function hueToHex(hue: number, saturation: number): string {
  const channel = (n: number) => {
    const k = (n + hue / 60) % 6;
    return Math.round(255 * (1 - saturation * Math.max(0, Math.min(k, 4 - k, 1)))).toString(16).padStart(2, "0");
  };
  return `#${channel(5)}${channel(3)}${channel(1)}`;
}
