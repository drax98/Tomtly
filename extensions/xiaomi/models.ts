// The models tuned by hand on real hardware. These tables override the driver built from a model's spec (spec.ts), and their
// codes, ranges and writable flags must not change, since scripts may use them. They are allowlists, so debug and factory
// properties stay out of reach. Property ids come from miot-spec.org.
// Left out on purpose everywhere: device information, firmware, country codes, debug and factory properties, reboot causes,
// filter RFID data, and filter or consumable resets, which can't be undone and sit one tap away on a generic card.

export interface Prop {
  code: string;
  siid: number;
  piid: number;
  type: "Boolean" | "Enum" | "Integer" | "Action" | "String" | "Number";
  writable: boolean;
  /** Protocol number to the name scripts see. */
  names?: Map<number, string>;
  scale: number;
  range?: string[];
  min?: number;
  max?: number;
  step?: number;
  unit?: string;
  /** Divides the raw value before scale, for readings reported in a tiny unit such as seconds or mm². */
  divide?: number;
  /** For an Action: each choice's [siid, aiid] in the MIoT spec. */
  actions?: Map<string, [number, number]>;
  /** "<siid>.<piid>", the id get_properties and set_properties use. */
  did: string;
  /** The spec lets this be written but not read, so it's never asked for. */
  writeOnly?: boolean;
  /** A 24-bit colour shown as #rrggbb, with its hue and saturation as numbers beside it. */
  colour?: "hex" | "hue" | "saturation";
  /** A read-only float kept to this many decimals, since a device's 21.040000000000003 means 21.04. */
  digits?: number;
  /** An older device's own commands, instead of siid and piid. */
  legacy?: LegacyCalls;
}

export interface LegacyCalls {
  /** The name in get_prop's list, or with call a method that answers on its own. */
  get: string;
  call?: boolean;
  /** Method that changes it, given the value, "on" or "off" for a Boolean, then with. */
  set?: string;
  /** For devices with a method each for on and off, which takes nothing. */
  setOn?: string;
  setOff?: string;
  with?: (string | number)[];
}

export function base(code: string, siid: number, piid: number, type: Prop["type"], writable: boolean): Prop {
  return { code, siid, piid, type, writable, scale: 0, did: `${siid}.${piid}` };
}

export function enumOf(code: string, siid: number, piid: number, writable: boolean, ...pairs: [number, string][]): Prop {
  return { ...base(code, siid, piid, "Enum", writable), names: new Map(pairs), range: pairs.map((p) => p[1]) };
}

export function onOff(code: string, siid: number, piid: number): Prop {
  return base(code, siid, piid, "Boolean", true);
}

export function flag(code: string, siid: number, piid: number): Prop {
  return base(code, siid, piid, "Boolean", false);
}

export function reading(code: string, siid: number, piid: number, unit: string): Prop {
  return { ...base(code, siid, piid, "Integer", false), unit };
}

function temperature(siid: number, piid: number): Prop {
  return { ...reading("temperature", siid, piid, "°C"), scale: 1 };
}

export function numberOf(code: string, siid: number, piid: number, min: number, max: number, unit: string): Prop {
  return { ...base(code, siid, piid, "Integer", true), min, max, step: 1, unit };
}

function commands(...list: [string, number, number][]): Prop {
  return { ...base("command", 0, 0, "Action", true), range: list.map((a) => a[0]), actions: new Map(list.map((a): [string, [number, number]] => [a[0], [a[1], a[2]]])) };
}

export const miotModels: Record<string, { kind: string; props: Prop[] }> = {
  // https://miot-spec.org/miot-spec-v2/instance?type=urn:miot-spec-v2:device:air-purifier:0000A007:zhimi-mb5:1
  "zhimi.airp.mb5": {
    kind: "air",
    props: [
      onOff("power", 2, 1),
      enumOf("mode", 2, 4, true, [0, "auto"], [1, "sleep"], [2, "favorite"], [3, "manual"]),
      enumOf("fan_level", 2, 5, true, [1, "1"], [2, "2"], [3, "3"]),
      numberOf("favorite_level", 9, 5, 0, 11, ""),
      onOff("anion", 2, 6),
      onOff("sound", 6, 1),
      onOff("child_lock", 8, 1),
      enumOf("display", 13, 2, true, [0, "off"], [1, "bright"], [2, "brightest"]),
      reading("pm25", 3, 4, "µg/m³"),
      reading("humidity", 3, 1, "%"),
      temperature(3, 7),
      reading("filter_life", 4, 1, "%"),
      reading("filter_days_left", 4, 4, "days"), // 4.3 is the hours used, not the days left
      reading("filter_hours_used", 4, 3, "h"),
      enumOf("fault", 2, 2, false, [0, "none"], [1, "PM sensor"], [2, "temperature sensor"], [3, "humidity sensor"], [4, "no filter"]),
      enumOf("temperature_unit", 14, 1, true, [1, "celsius"], [2, "fahrenheit"]), // only what the unit's screen shows
      numberOf("favorite_speed", 9, 2, 200, 2300, "rpm"),
      enumOf("manual_level", 9, 9, true, [1, "1"], [2, "2"], [3, "3"]),
      reading("motor_speed", 9, 1, "rpm"),
      reading("motor_target_speed", 9, 4, "rpm"),
      flag("bottom_door_open", 9, 6),
      reading("purified_volume", 11, 1, "m³"), // the unit from python-miio, the spec gives none
      reading("average_aqi", 11, 2, ""),
      enumOf("aqi_state", 11, 3, false, [0, "good, low"], [1, "good, high"], [2, "moderate, low"], [3, "moderate, high"], [4, "bad, low"], [5, "bad, high"]),
      commands(["toggle", 2, 1], ["next_mode", 9, 1], ["next_fan_level", 9, 2]),
    ],
  },
  // https://miot-spec.org/miot-spec-v2/instance?type=urn:miot-spec-v2:device:humidifier:0000A00E:zhimi-ca6:1
  "zhimi.humidifier.ca6": {
    kind: "air",
    props: [
      onOff("power", 2, 1),
      enumOf("mode", 2, 5, true, [0, "favorite"], [1, "auto"], [2, "sleep"]),
      numberOf("target_humidity", 2, 6, 30, 60, "%"),
      onOff("auto_dry", 2, 8),
      onOff("sound", 4, 1),
      onOff("child_lock", 6, 1),
      enumOf("display", 5, 2, true, [0, "off"], [1, "dim"], [2, "bright"]),
      enumOf("status", 2, 9, false, [1, "off"], [2, "working"], [3, "drying"], [4, "cleaning"]),
      reading("humidity", 3, 9, "%"),
      temperature(3, 7),
      enumOf("water_level", 2, 7, false, [0, "empty"], [1, "normal"], [2, "full"]), // spec range 0 to 2, names from python-miio
      reading("fault", 2, 2, ""),
      onOff("self_clean", 7, 5),
      reading("self_clean_minutes", 7, 6, "min"), // the spec calls it a percent but gives minutes, 0 to 30
      reading("actual_speed", 7, 1, "rpm"),
      flag("pump_running", 7, 7),
      reading("pump_count", 7, 8, ""),
      commands(["toggle", 2, 1], ["next_mode", 7, 1]),
    ],
  },
  // https://miot-spec.org/miot-spec-v2/instance?type=urn:miot-spec-v2:device:fan:0000A005:dmaker-1c:1
  "dmaker.fan.1c": {
    kind: "fan",
    props: [
      onOff("power", 2, 1),
      enumOf("fan_level", 2, 2, true, [1, "1"], [2, "2"], [3, "3"]),
      enumOf("mode", 2, 7, true, [0, "straight"], [1, "sleep"]),
      onOff("swing", 2, 3),
      numberOf("off_timer", 2, 10, 0, 480, "min"),
      onOff("sound", 2, 11),
      onOff("light", 2, 12),
      onOff("child_lock", 3, 1),
      commands(["toggle", 2, 1]),
    ],
  },
  // https://miot-spec.org/miot-spec-v2/instance?type=urn:miot-spec-v2:device:humidifier:0000A00E:deerma-jsq5:1
  "deerma.humidifier.jsq5": {
    kind: "air",
    props: [
      onOff("power", 2, 1),
      enumOf("mode", 2, 5, true, [1, "1"], [2, "2"], [3, "3"], [4, "humidity"]),
      numberOf("target_humidity", 2, 6, 40, 80, "%"),
      onOff("sound", 5, 1),
      onOff("light", 6, 1),
      reading("humidity", 3, 1, "%"),
      reading("temperature", 3, 7, "°C"),
      flag("no_water", 7, 1),
      flag("tank_removed", 7, 2),
      enumOf("fault", 2, 2, false, [0, "none"], [1, "no water"], [2, "tank removed"]),
    ],
  },
  // https://miot-spec.org/miot-spec-v2/instance?type=urn:miot-spec-v2:device:light:0000A001:yeelink-ceiling5:1
  "yeelink.light.ceiling5": {
    kind: "light",
    props: [
      onOff("power", 2, 1),
      numberOf("brightness", 2, 2, 1, 100, "%"),
      numberOf("color_temperature", 2, 4, 2700, 5700, "K"),
      enumOf("mode", 2, 3, true, [1, "day"], [2, "night"]),
      onOff("flex_switch", 2, 5),
      commands(["toggle", 2, 1]),
    ],
  },
};

// Roborock vacuums answer get_properties with "unknown_method", so they use their own commands.
export const roborockModels: Record<string, boolean> = { "roborock.vacuum.s5": true };

export const roborockActions: Record<string, string[]> = {
  start: ["app_start"],
  pause: ["app_pause"],
  stop: ["app_stop"],
  dock: ["app_pause", "app_charge"], // the vacuum ignores app_charge while it is cleaning
  spot: ["app_spot"],
  find: ["find_me"],
};

export const roborockProps: Prop[] = [
  { ...base("command", 0, 0, "Action", true), range: ["start", "pause", "stop", "dock", "spot", "find"] },
  // 105 isn't in the MIoT spec; python-miio's FanspeedV2 lists it for this model, the app's mop setting.
  enumOf("fan_speed", 0, 1, true, [101, "silent"], [102, "balanced"], [103, "strong"], [104, "max"], [105, "gentle"]),
  enumOf("state", 0, 2, false, [1, "starting"], [2, "off the dock"], [3, "idle"], [4, "remote control"], [5, "cleaning"],
    [6, "returning"], [7, "manual"], [8, "charging"], [9, "charging problem"], [10, "paused"], [11, "spot cleaning"], [12, "error"],
    [13, "shutting down"], [14, "updating"], [15, "docking"], [16, "going to target"], [17, "zone cleaning"], [18, "room cleaning"], [100, "charged"]),
  reading("battery", 0, 3, "%"),
  reading("error_code", 0, 4, ""),
  { ...base("sound_volume", 0, 5, "Integer", true), min: 0, max: 100, step: 1, unit: "%" },
  // Raw units and part lifetimes per python-miio's VacuumStatus, CleaningSummary and ConsumableStatus.
  { ...reading("clean_time", 0, 6, "min"), divide: 60 },
  { ...reading("clean_area", 0, 7, "m²"), divide: 1_000_000 },
  flag("do_not_disturb", 0, 8),
  flag("map_present", 0, 9),
  { ...reading("total_clean_hours", 0, 10, "h"), divide: 3600 },
  { ...reading("total_clean_area", 0, 11, "m²"), divide: 1_000_000 },
  reading("clean_count", 0, 12, ""),
  { ...reading("main_brush_hours", 0, 13, "h"), divide: 3600 }, // replace at 300
  { ...reading("side_brush_hours", 0, 14, "h"), divide: 3600 }, // replace at 200
  { ...reading("filter_hours", 0, 15, "h"), divide: 3600 }, // replace at 150
  { ...reading("sensor_dirty_hours", 0, 16, "h"), divide: 3600 }, // clean at 30
];

/** Maps the vacuum's get_status fields to its values. */
export const roborockStatus: [string, string][] = [
  ["fan_speed", "fan_power"], ["state", "state"], ["battery", "battery"], ["error_code", "error_code"],
  ["clean_time", "clean_time"], ["clean_area", "clean_area"], ["do_not_disturb", "dnd_enabled"], ["map_present", "map_present"],
];

/** Other read commands and the fields they fill. A list reply is read by position, as older firmware sends them. */
export const roborockExtras: { method: string; fields: [string, string | number][] }[] = [
  { method: "get_consumable", fields: [["main_brush_hours", "main_brush_work_time"], ["side_brush_hours", "side_brush_work_time"], ["filter_hours", "filter_work_time"], ["sensor_dirty_hours", "sensor_dirty_time"]] },
  { method: "get_clean_summary", fields: [["total_clean_hours", "clean_time"], ["total_clean_area", "clean_area"], ["clean_count", "clean_count"]] },
  { method: "get_sound_volume", fields: [["sound_volume", 0]] },
];
