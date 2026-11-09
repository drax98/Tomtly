// Older miIO devices, which have no MIoT properties and answer their own commands instead. Each family is a table of the
// names they use, written here from what python-miio's source shows of the protocol (https://github.com/rytilahti/python-miio,
// GPL-3.0; none of its code is used) under miio/integrations/: yeelight/light, zhimi/airpurifier, zhimi/humidifier,
// chuangmi/plug, philips/light, zhimi/fan and lumi/gateway. None of this has run on a real device.
import { base, enumOf, flag, numberOf, onOff, Prop, reading } from "./models";

export interface Family {
  match: RegExp;
  kind: string;
  props: Prop[];
}

const on = (code: string, get: string, set: string, ...with_: (string | number)[]): Prop => ({ ...onOff(code, 0, 0), legacy: { get, set, with: with_ } });
const number = (code: string, get: string, set: string, min: number, max: number, unit: string, ...with_: (string | number)[]): Prop => ({ ...numberOf(code, 0, 0, min, max, unit), legacy: { get, set, with: with_ } });
const shows = (code: string, get: string, unit: string): Prop => ({ ...reading(code, 0, 0, unit), legacy: { get } });
const state = (code: string, get: string): Prop => ({ ...flag(code, 0, 0), legacy: { get } });
const tenths = (code: string, get: string, unit: string): Prop => ({ ...shows(code, get, unit), scale: 1 });

function mode(set: string, ...names: string[]): Prop {
  return { ...enumOf("mode", 0, 0, true), range: names, legacy: { get: "mode", set } };
}

const SMOOTH = ["smooth", 500]; // how python-miio's Yeelight changes a light
const PHILIPS: Prop[] = [on("power", "power", "set_power"), number("brightness", "bright", "set_bright", 1, 100, "%"), number("color_temperature", "cct", "set_cct", 1, 100, "%"), shows("scene", "snm", "")];
const FAN_READINGS: Prop[] = [tenths("temperature", "temp_dec", "°C"), shows("humidity", "humidity", "%"), shows("battery", "battery", "%")];

// shortcut: Yeelight colour is only shown, and an older light's background light and moonlight aren't read; add set_rgb, bg_* and nl_br when someone with one asks.
export const FAMILIES: Family[] = [
  {
    match: /^yeelink\.(light|bhf_light)\./,
    kind: "light",
    props: [
      on("power", "power", "set_power", ...SMOOTH),
      number("brightness", "bright", "set_bright", 1, 100, "%", ...SMOOTH),
      number("color_temperature", "ct", "set_ct_abx", 1700, 6500, "K", ...SMOOTH),
      { ...base("color", 0, 0, "String", false), colour: "hex", legacy: { get: "rgb" } },
      shows("color_mode", "color_mode", ""),
      state("flowing", "flowing"),
    ],
  },
  { match: /^philips\.light\.(bulb|cbulb|candle2?|downlight|ceiling|zyceiling)$/, kind: "light", props: PHILIPS },
  {
    match: /^zhimi\.airpurifier\.(v[1-7]|m[12]|ma[12]|sa[12]|mc[12])$/,
    kind: "air",
    props: [
      on("power", "power", "set_power"),
      mode("set_mode", "auto", "silent", "favorite", "idle"),
      number("favorite_level", "favorite_level", "set_level_favorite", 0, 17, ""),
      on("sound", "buzzer", "set_buzzer"),
      on("child_lock", "child_lock", "set_child_lock"),
      on("display", "led", "set_led"),
      shows("pm25", "aqi", "µg/m³"),
      shows("average_aqi", "average_aqi", ""),
      shows("humidity", "humidity", "%"),
      tenths("temperature", "temp_dec", "°C"),
      shows("filter_life", "filter1_life", "%"),
      shows("filter_hours_used", "f1_hour_used", "h"),
      shows("motor_speed", "motor1_speed", "rpm"),
      shows("purified_volume", "purify_volume", "m³"),
    ],
  },
  {
    match: /^zhimi\.humidifier\.(v1|ca1|cb1|cb2)$/,
    kind: "air",
    props: [
      on("power", "power", "set_power"),
      mode("set_mode", "silent", "medium", "high", "auto", "strong"),
      number("target_humidity", "limit_hum", "set_limit_hum", 30, 80, "%"),
      on("sound", "buzzer", "set_buzzer"),
      on("child_lock", "child_lock", "set_child_lock"),
      shows("humidity", "humidity", "%"),
      tenths("temperature", "temp_dec", "°C"), // v1 and ca1
      { ...base("temperature_now", 0, 0, "Number", false), unit: "°C", digits: 1, legacy: { get: "temperature" } }, // cb1 and cb2
      shows("water_depth", "depth", ""),
      on("auto_dry", "dry", "set_dry"),
    ],
  },
  {
    match: /^chuangmi\.plug\.(v1|v3)$/,
    kind: "plug",
    props: [
      { ...onOff("power", 0, 0), legacy: { get: "on", setOn: "set_on", setOff: "set_off" } },
      { ...onOff("usb", 0, 0), legacy: { get: "usb_on", setOn: "set_usb_on", setOff: "set_usb_off" } },
      shows("temperature", "temperature", "°C"),
    ],
  },
  { match: /^chuangmi\.plug\.(m1|m3|v2|hmi205|hmi206|hmi208)$/, kind: "plug", props: [on("power", "power", "set_power"), shows("temperature", "temperature", "°C")] },
  {
    match: /^zhimi\.fan\.(v2|v3|sa1|za1|za3|za4)$/,
    kind: "fan",
    props: [
      on("power", "power", "set_power"),
      number("speed", "speed_level", "set_speed_level", 0, 100, "%"),
      number("natural_speed", "natural_level", "set_natural_level", 0, 100, "%"),
      on("swing", "angle_enable", "set_angle_enable"),
      number("swing_angle", "angle", "set_angle", 1, 120, "°"),
      on("child_lock", "child_lock", "set_child_lock"),
      ...FAN_READINGS,
    ],
  },
  {
    // Only what the gateway itself answers. Its Zigbee children use a different set of calls, and arming isn't changed from here.
    match: /^lumi\.(gateway\.(v3|mieu01|mgl03|aqhm01)|acpartner\.v[123])$/,
    kind: "infrastructure",
    props: [
      { ...base("arming", 0, 0, "Boolean", false), legacy: { get: "get_arming", call: true } },
      { ...base("light_color", 0, 0, "String", false), colour: "hex", legacy: { get: "get_rgb", call: true } },
    ],
  },
];

export function familyFor(model: string): Family | undefined {
  return FAMILIES.find((f) => f.match.test(model));
}
