// What can be written and how values scale. The cloud mapping has no read/write flag, so this goes by what Tuya documents
// the codes to mean, in its instruction sets ("can be sent") and status sets ("only reported"):
// https://developer.tuya.com/en/docs/iot/standarddescription?id=K9i5ql6waswzq
import { CATEGORIES, kindOf } from "./categories";

export interface Mapping {
  code: string;
  type: string;
  values?: unknown;
}

// Whole categories that only report. Locks and safes are here on purpose: remote unlocking needs a cloud ticket anyway, and
// a wrong guess must not open a door. Cameras too: their settings are for the app, and what they stream isn't on this protocol.
const REPORT_ONLY_CATEGORIES = [
  "mcs", "pir", "hps", "zd", "wsdcg", "qxj", "ldcg", "ylcg", "pm2.5", "voc", "hjjcy", "szjcy", "ywcgq", "zwjcy", "znsb", "wxkg", // sensors
  "ywbj", "cobj", "co2bj", "rqbj", "jwbj", "jqbj", "dgnbj", "sj", "sos", // alarms that only detect
  "ms", "jtmspro", "jtmsbh", "gyms", "hotelms", "ms_category", "videolock", "photolock", "mk", "bxx", // locks
  "sp", "dghsxj", // cameras
  "zndb", "swtz", "tzc1", "sb", "tracker", "ykq", // meters and things that send readings
];

// Destructive or pairing codes, never writable, in any category. A separated word, so "unbind" and "bind" match and "binding_x" doesn't.
const BLOCKED_CODES = /(^|_)(factory|reset|restore|restart|reboot|format|pair|pairing|bind|unbind|ota|upgrade|firmware|wipe|erase|delete|clear|unlock|wifi|network|zigbee)(_|$|\d)/;

// The rule tuned to the first home this ran in. These categories keep it exactly, so its devices don't change.
// shortcut: wk still offers valve_state and window_state as writable, drop wk from this list once that home's mapping is checked.
const TUNED_CATEGORIES = ["wk", "dd", "cz", "cl", "pc", "tdq", "wg2"];
const TUNED_READ_ONLY_CODES = /^(cur_|battery|temp_current|add_ele|fault|work_state|percent_state|checking_result|temper_alarm|co_status|co_value|smoke_sensor_status|relay_status|master_state|heating_state|open$)/;

// Readings, by Tuya's naming. "_value" alone isn't one: bright_value and temp_value are commands on lights.
const REPORTED_CODES = new RegExp("^(" + [
  "cur_", "battery", "residual", "electricity_left", "add_ele", "fault", "open$", "checking_result", "temper_alarm", "wireless_",
  "temp_(current|indoor|outdoor|around|effluent|venting|coiler|in$|out$)", "humidity_(current|value|indoor|outdoor)", "ext_temp", "sensor_", "va_",
  "dew_point", "feellike", "heat_index", "windchill", "windspeed", "wind_direct", "rain_", "uv_index", "atmospheric", "pressure_value", "bright_state", "illuminance",
  "pm\\d", "tvoc", "voc_", "eco2", "co2_", "ch2o_", "ch4_", "co_", "gas_sensor", "smoke_sensor", "watersensor", "air_quality", "pir", "presence", "sos_state", "shock_state", "vibration",
  "alarm_(message|msg|state)", "doorbell", "movement_detect", "doorcontact", "hijack", "lock_motor", "closed_opened",
  "phase_", "forward_energy", "reverse_energy", "total_", "today_", "power_total", "pv_power", "current_soc", "charge_energy", "cumulative_", "cuml_",
  "clean_area", "clean_time", "time_use", "time_total", "use_time", "remain_time", "countdown_left", "work_power", "filter$", "filter_(life|days|duration)", "feed_report",
  "cat_weight", "excretion_", "liquid_", "tds_", "orp_", "ec_", "switch_mode\\d",
].join("|") + ")|(_state|_status|_current|_left|_total|_count|_report|_pic)$|^(state|status)$");

// A category nobody documented could be anything, so only what's plainly a command there.
const UNKNOWN_CATEGORY_COMMANDS = /^(switch(_\d+|_led(_\d+)?)?|power|bright_value(_\d|_v2)?|temp_value(_v2)?|work_mode|mode|percent_control(_\d)?|control(_\d)?|temp_set|fan_speed(_enum|_percent)?|speed|child_lock|countdown(_\d+)?)$/;

/** By category and code alone. The value's type is checked by the caller. */
export function codeWritable(category: string, code: string): boolean {
  if (BLOCKED_CODES.test(code) || REPORT_ONLY_CATEGORIES.includes(category)) return false;
  if (TUNED_CATEGORIES.includes(category)) return !TUNED_READ_ONLY_CODES.test(code);
  if (REPORTED_CODES.test(code)) return false;
  return Object.prototype.hasOwnProperty.call(CATEGORIES, category) || category.startsWith("wg") || UNKNOWN_CATEGORY_COMMANDS.test(code);
}

/**
 * The mapping's own scale, except two codes where one home's cloud mapping says 0 while the devices report tenths
 * (210 = 21.0 °C, 2340 = 234.0 V). Elsewhere only the limits can show that: no thermostat reads to 200 °C, no mains to 1000 V.
 * A device whose mapping says 0 with smaller limits really reports whole numbers.
 */
export function scaleOf(category: string, code: string, values: Record<string, any>): number | undefined {
  const cloud = values.scale;
  // shortcut: a tuned wk or cz reporting whole numbers reads a tenth of its value; narrow this with the limits once that home's mapping is checked.
  if (TUNED_CATEGORIES.includes(category) && (code === "temp_current" || code === "cur_voltage")) return 1;
  if (!cloud && code === "temp_current" && kindOf(category) === "climate" && !(values.max < 200)) return 1;
  if (!cloud && code === "cur_voltage" && !(values.max < 1000)) return 1;
  return typeof cloud === "number" && Number.isInteger(cloud) && cloud !== 0 ? cloud : undefined;
}

// Tuya's thing model says bool and value, its standard sets say Boolean and Integer. Both turn up in the file.
const TYPES: Record<string, string> = { bool: "Boolean", boolean: "Boolean", value: "Integer", integer: "Integer", enum: "Enum", string: "String", raw: "Raw", json: "Json", bitmap: "Bitmap" };

/** Makes the wizard's mapping safe to use, and corrects what it gets wrong per Tuya's thing model. A code must be unique, or the whole list is refused. */
export function cleanMapping(category: string, mapping: unknown): Record<string, Mapping> {
  const out: Record<string, Mapping> = {};
  const raw = mapping !== null && typeof mapping === "object" ? mapping as Record<string, any> : {};
  const seen: Record<string, boolean> = {};
  for (const dp of Object.keys(raw)) {
    const m = raw[dp];
    if (m === null || typeof m !== "object") continue;
    let values = m.values;
    if (typeof values === "string") {
      try {
        values = JSON.parse(values); // String and Raw data points carry "" or a JSON string
      } catch {
        values = {};
      }
    }
    let code = typeof m.code === "string" && m.code !== "" ? m.code : `dp_${dp}`;
    // These sensors name it prm_switch or switch in the mapping, and true means the window is open.
    if (category === "mcs" && dp === "101") code = "open";
    if (seen[code]) code = `${code}_${dp}`;
    seen[code] = true;
    const type = String(m.type).toLowerCase();
    out[dp] = { code, type: Object.prototype.hasOwnProperty.call(TYPES, type) ? TYPES[type] : "String", values };
  }
  // heating_state is missing from the mapping of these thermostats, and the device sends it.
  if (category === "wk" && out["102"] === undefined && !seen.heating_state) out["102"] = { code: "heating_state", type: "Boolean" };
  // Scripts read "open" from any window sensor.
  if (category === "mcs" && !Object.values(out).some((m) => m.code === "open")) {
    const contact = Object.values(out).find((m) => m.code === "doorcontact_state");
    if (contact !== undefined) contact.code = "open";
  }
  return out;
}
