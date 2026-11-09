import { describe, expect, it } from "vitest";
import { CATEGORIES, kindOf } from "../tuya/categories";
import { colourMax, colourWords, formatColour, isColourCode, parseColour } from "../tuya/colour";
import { crc32, decode, dpsOf, hasSessionKey, newSession, nextFrame, control, statusRead, type Frame, type Session } from "../tuya/protocol";
import { cleanMapping, codeWritable, scaleOf } from "../tuya/rules";
import { crypto, FakeConnection } from "./harness";

// The pure parts of the Tuya extension: the category table, the mapping and writable rules, colour, and the protocol
// functions that take no device. The device-level behaviour is in tuya.test.ts.

describe("tuya categories", () => {
  it("gives every listed category a kind, and any gateway a gateway kind", () => {
    expect(kindOf("dj")).toBe("light");
    expect(kindOf("cz")).toBe("plug");
    expect(kindOf("mcs")).toBe("sensor");
    expect(kindOf("wg2")).toBe("infrastructure");
    expect(kindOf("wg9")).toBe("infrastructure");
    expect(kindOf("pm2.5")).toBe("sensor");
    expect(Object.keys(CATEGORIES)).toContain("bzyd");
  });

  it("reads an unknown category as other", () => {
    expect(kindOf("zz")).toBe("other");
    expect(kindOf("")).toBe("other");
  });

  it("treats a category named like an object's own property as unknown", () => {
    expect(kindOf("constructor")).toBe("other");
    expect(kindOf("toString")).toBe("other");
  });
});

describe("tuya writable rules", () => {
  it("never writes the destructive or pairing codes, in any category", () => {
    for (const code of ["factory_reset", "reset", "restart", "bind", "unbind", "pair", "wifi_mode", "zigbee_join", "firmware_upgrade", "ota"]) {
      expect(codeWritable("dj", code), code).toBe(false);
    }
  });

  it("doesn't mistake an ordinary code for a blocked one that merely contains a blocked word", () => {
    expect(codeWritable("dj", "binding_x")).toBe(true);
    expect(codeWritable("dj", "bright_value")).toBe(true);
  });

  it("never writes a report-only category, such as locks, sensors and cameras", () => {
    expect(codeWritable("ms", "switch_1")).toBe(false);
    expect(codeWritable("mcs", "switch")).toBe(false);
    expect(codeWritable("sp", "switch")).toBe(false);
  });

  it("uses the tuned rule for the categories tuned on the first home", () => {
    expect(codeWritable("wk", "switch")).toBe(true);
    expect(codeWritable("wk", "temp_current")).toBe(false);
    expect(codeWritable("cz", "switch_1")).toBe(true);
    expect(codeWritable("cz", "cur_voltage")).toBe(false);
  });

  it("never writes a reading, by its name", () => {
    expect(codeWritable("dj", "cur_current")).toBe(false);
    expect(codeWritable("dj", "heating_state")).toBe(false);
    expect(codeWritable("dj", "electricity_left")).toBe(false);
    expect(codeWritable("dj", "switch_mode1")).toBe(false);
  });

  it("writes a listed category's codes that aren't readings", () => {
    expect(codeWritable("dj", "work_mode")).toBe(true);
    expect(codeWritable("kt", "temp_set")).toBe(true);
  });

  it("writes an unlisted category only what's plainly a command, and any gateway's codes", () => {
    expect(codeWritable("zz", "switch_1")).toBe(true);
    expect(codeWritable("zz", "xyz")).toBe(false);
    expect(codeWritable("wg9", "switch_1")).toBe(true);
  });

  it("doesn't treat a category named like an object's own property as listed, so an unknown code stays read-only", () => {
    expect(codeWritable("constructor", "xyz")).toBe(false);
    expect(codeWritable("toString", "xyz")).toBe(false);
    expect(codeWritable("constructor", "switch_1")).toBe(true); // plainly a command, as for any unlisted category
  });
});

describe("tuya scale", () => {
  it("reads tenths for a tuned climate or mains code, whatever the mapping says", () => {
    expect(scaleOf("wk", "temp_current", { scale: 0 })).toBe(1);
    expect(scaleOf("cz", "cur_voltage", {})).toBe(1);
  });

  it("reads tenths for a climate temperature whose limits are 200 or more, and for mains of 1000 V or more", () => {
    expect(scaleOf("kt", "temp_current", { max: 500 })).toBe(1);
    expect(scaleOf("kt", "temp_current", { max: 150 })).toBeUndefined();
    expect(scaleOf("kt", "temp_current", {})).toBe(1);
    expect(scaleOf("dj", "cur_voltage", { max: 2500 })).toBe(1);
    expect(scaleOf("dj", "cur_voltage", { max: 500 })).toBeUndefined();
  });

  it("uses the mapping's own scale when it's a whole, nonzero number", () => {
    expect(scaleOf("dj", "cur_current", { scale: 3 })).toBe(3);
    expect(scaleOf("dj", "cur_current", { scale: 0 })).toBeUndefined();
    expect(scaleOf("dj", "cur_current", { scale: 1.5 })).toBeUndefined();
    expect(scaleOf("dj", "cur_current", {})).toBeUndefined();
  });
});

describe("tuya mapping cleanup", () => {
  it("reads a mapping that isn't an object as empty, and skips entries that aren't objects", () => {
    expect(cleanMapping("dj", null)).toEqual({});
    expect(cleanMapping("dj", "x")).toEqual({});
    expect(cleanMapping("dj", { "1": null, "2": 5, "3": { code: "switch_1", type: "Bool" } })).toEqual({ "3": { code: "switch_1", type: "Boolean", values: undefined } });
  });

  it("parses values sent as JSON text, and reads text that isn't JSON as empty", () => {
    const out = cleanMapping("dj", { "1": { code: "a", type: "Enum", values: '{"range":["x"]}' }, "2": { code: "b", type: "Raw", values: "{not json" }, "3": { code: "c", type: "Raw", values: "" } });
    expect(out["1"].values).toEqual({ range: ["x"] });
    expect(out["2"].values).toEqual({});
    expect(out["3"].values).toEqual({});
  });

  it("names a data point with no code by its number, and gives a repeated code a suffix", () => {
    const out = cleanMapping("dj", { "7": { code: "", type: "Boolean" }, "8": { type: "Boolean" }, "9": { code: "switch_1", type: "Boolean" }, "10": { code: "switch_1", type: "Boolean" } });
    expect(Object.values(out).map((m) => m.code)).toEqual(["dp_7", "dp_8", "switch_1", "switch_1_10"]);
  });

  it("maps the type names of both thing models, and reads anything else as text", () => {
    const types = ["bool", "boolean", "value", "integer", "enum", "string", "raw", "json", "bitmap", "action"];
    const out = cleanMapping("dj", Object.fromEntries(types.map((t, i) => [String(i + 1), { code: `c${i}`, type: t }])));
    expect(Object.values(out).map((m) => m.type)).toEqual(["Boolean", "Boolean", "Integer", "Integer", "Enum", "String", "Raw", "Json", "Bitmap", "String"]);
  });

  it("reads a type named like an object's own property as text", () => {
    const out = cleanMapping("dj", { "1": { code: "a", type: "constructor" }, "2": { code: "b", type: "toString" } });
    expect(out["1"].type).toBe("String");
    expect(out["2"].type).toBe("String");
  });

  it("adds heating_state to a thermostat that doesn't map it, and not otherwise", () => {
    expect(cleanMapping("wk", {})).toEqual({ "102": { code: "heating_state", type: "Boolean" } });
    expect(cleanMapping("wk", { "102": { code: "x", type: "Boolean" } })["102"].code).toBe("x");
    expect(cleanMapping("wk", { "9": { code: "heating_state", type: "Boolean" } })["102"]).toBeUndefined();
    expect(cleanMapping("dj", {})).toEqual({});
  });

  it("names a window sensor's contact 'open', for the cloud's names and for a sensor with a switch", () => {
    expect(cleanMapping("mcs", { "101": { code: "prm_switch", type: "Boolean" } })["101"].code).toBe("open");
    expect(cleanMapping("mcs", { "1": { code: "doorcontact_state", type: "Boolean" } })["1"].code).toBe("open");
    const kept = cleanMapping("mcs", { "1": { code: "doorcontact_state", type: "Boolean" }, "2": { code: "open", type: "Boolean" } });
    expect(kept["1"].code).toBe("doorcontact_state");
    expect(cleanMapping("mcs", { "1": { code: "other", type: "Boolean" } })["1"].code).toBe("other");
  });
});

describe("tuya colour", () => {
  it("recognises the colour data points, and no other", () => {
    expect(isColourCode("colour_data")).toBe(true);
    expect(isColourCode("color_data")).toBe(true);
    expect(isColourCode("colour_data_v2")).toBe(true);
    expect(isColourCode("colour_data_hsv")).toBe(true);
    expect(isColourCode("colour_data_x")).toBe(false);
  });

  it("parses the three forms a light sends, and nothing else", () => {
    expect(parseColour("00dc000003e8")).toEqual({ form: "hsv16", h: 220, s: 0, v: 1000 });
    expect(parseColour("ff00000000ffff")).toEqual({ form: "rgb8", h: 0, s: 255, v: 255 });
    expect(parseColour('{"h":10,"s":500,"v":500}')).toEqual({ form: "json", h: 10, s: 500, v: 500 });
    expect(parseColour({ h: 10, s: 500, v: 500 })).toEqual({ form: "object", h: 10, s: 500, v: 500 });
    expect(parseColour("not json")).toBeNull();
    expect(parseColour("5")).toBeNull();
    expect(parseColour({ h: 1, s: 2 })).toBeNull();
    expect(parseColour(null)).toBeNull();
    expect(parseColour(7)).toBeNull();
  });

  it("tops saturation and brightness at the mapping's own limit, else at 255 for RGB and JSON, else at 1000", () => {
    expect(colourMax({ form: "hsv16", h: 0, s: 0, v: 0 }, "colour_data", { s: { max: 100 } })).toBe(100);
    expect(colourMax({ form: "hsv16", h: 0, s: 0, v: 0 }, "colour_data", { s: { max: 0 } })).toBe(1000);
    expect(colourMax({ form: "rgb8", h: 0, s: 0, v: 0 }, "colour_data", {})).toBe(255);
    expect(colourMax({ form: "json", h: 0, s: 0, v: 0 }, "colour_data", {})).toBe(255);
    expect(colourMax({ form: "hsv16", h: 0, s: 0, v: 0 }, "colour_data", {})).toBe(1000);
    expect(colourMax({ form: "object", h: 0, s: 0, v: 0 }, "colour_data_v2", {})).toBe(1000);
  });

  it("writes each form back as it came", () => {
    expect(formatColour({ form: "object", h: 1, s: 2, v: 3 })).toEqual({ h: 1, s: 2, v: 3 });
    expect(formatColour({ form: "json", h: 1, s: 2, v: 3 })).toBe('{"h":1,"s":2,"v":3}');
    expect(formatColour({ form: "hsv16", h: 220, s: 0, v: 1000 })).toBe("00dc000003e8");
    expect(formatColour({ form: "hsv16", h: 220.4, s: 0, v: 999.6 })).toBe("00dc000003e8");
  });

  it("writes an RGB light's colour as RGB, with the hue, saturation and value after it", () => {
    expect(formatColour({ form: "rgb8", h: 0, s: 255, v: 255 })).toBe("ff00000000ffff");
    expect(formatColour({ form: "rgb8", h: 120, s: 255, v: 255 })).toBe("00ff000078ffff");
    expect(formatColour({ form: "rgb8", h: 240, s: 255, v: 255 })).toBe("0000ff00f0ffff");
    expect(formatColour({ form: "rgb8", h: 0, s: 0, v: 255 })).toBe("ffffff000000ff");
  });

  it("says a colour in words, with saturation and brightness as percentages of their top", () => {
    expect(colourWords({ form: "object", h: 220, s: 1000, v: 500 }, 1000)).toBe("hue 220, saturation 100%, brightness 50%");
    expect(colourWords({ form: "object", h: 0, s: 1, v: 3 }, 3)).toBe("hue 0, saturation 33.3%, brightness 100%");
  });
});

describe("tuya protocol functions that need no device", () => {
  const session = (version: string, key = "0123456789abcdef"): Session => {
    const conn = new FakeConnection("192.168.1.50", 6668);
    return newSession({ crypto }, conn, version, key);
  };

  it("checks the CRC-32 of the IEEE polynomial, as the devices and Go's crc32 do", () => {
    expect(crc32(Buffer.from("123456789").toString("hex"))).toBe(0xcbf43926);
    expect(crc32("")).toBe(0);
  });

  it("starts a session with the local key as hex, and knows which versions have a session key", () => {
    const s = session("3.4");
    expect(s.realKey).toBe(Buffer.from("0123456789abcdef").toString("hex"));
    expect(s.key).toBe(s.realKey);
    expect(hasSessionKey("3.4")).toBe(true);
    expect(hasSessionKey("3.5")).toBe(true);
    expect(hasSessionKey("3.3")).toBe(false);
  });

  it("builds a status read for each kind of device", () => {
    const t = Math.floor(Date.now() / 1000);
    expect(statusRead("3.3", false, "id", undefined, [])).toEqual({ cmd: 0x0a, body: { gwId: "id", devId: "id", uid: "id", t: String(t) } });
    expect(statusRead("3.3", false, "id", "c1", [])).toEqual({ cmd: 0x0a, body: { t, cid: "c1" } });
    expect(statusRead("3.2", true, "id", undefined, [])).toEqual({ cmd: 0x0d, body: { devId: "id", uid: "id", t: String(t), dps: { "1": null } } });
    expect(statusRead("3.2", true, "id", "c1", ["2", "3"])).toEqual({ cmd: 0x0d, body: { devId: "id", uid: "id", t: String(t), dps: { "2": null, "3": null }, cid: "c1" } });
    expect(statusRead("3.4", false, "id", undefined, [])).toEqual({ cmd: 0x10, body: {} });
    expect(statusRead("3.5", false, "id", "c1", [])).toEqual({ cmd: 0x10, body: { cid: "c1" } });
  });

  it("builds a control for each kind of device", () => {
    const t = Math.floor(Date.now() / 1000);
    expect(control("3.3", "id", undefined, { "1": true })).toEqual({ cmd: 7, body: { devId: "id", uid: "id", t: String(t), dps: { "1": true } } });
    expect(control("3.3", "id", "c1", { "1": true })).toEqual({ cmd: 7, body: { t, cid: "c1", dps: { "1": true } } });
    expect(control("3.4", "id", undefined, { "1": true })).toEqual({ cmd: 0x0d, body: { protocol: 5, t, data: { dps: { "1": true } } } });
    expect(control("3.4", "id", "c1", { "1": true })).toEqual({ cmd: 0x0d, body: { protocol: 5, t, cid: "c1", data: { dps: { "1": true }, cid: "c1", ctype: 0 } } });
  });

  it("finds the data points and the sub-device id, from the top level or from data", () => {
    expect(dpsOf({ dps: { "1": true }, cid: "c" })).toEqual({ dps: { "1": true }, cid: "c" });
    expect(dpsOf({ data: { dps: { "2": 1 }, cid: "d" } })).toEqual({ dps: { "2": 1 }, cid: "d" });
    expect(dpsOf({ dps: null, data: { dps: { "3": 1 } } })).toEqual({ dps: { "3": 1 }, cid: "" });
    expect(dpsOf({ dps: null, data: null })).toEqual({ dps: undefined, cid: "" });
    expect(dpsOf({ dps: 5, cid: 7 })).toEqual({ dps: undefined, cid: "" });
    expect(dpsOf({ dps: {}, cid: "" })).toEqual({ dps: {}, cid: "" });
  });

  it("decodes nothing from an empty payload, from a 3.1 message that isn't JSON, and from a clear 'data unvalid' on 3.2", () => {
    const ctx = { crypto };
    expect(decode(ctx, session("3.3"), { cmd: 8, payload: "" })).toBeNull();
    const plain31 = session("3.1");
    expect(decode(ctx, plain31, { cmd: 8, payload: crypto.hex("hi") })).toBeNull();
    expect(plain31.proven).toBe(false);
    const clear = session("3.2");
    expect(decode(ctx, clear, { cmd: 8, payload: crypto.hex("json obj data unvalid") })).toBeNull();
    expect(clear.unvalid).toBe(true);
    expect(clear.proven).toBe(false);
  });

  it("reads the next frame only once it's whole, and refuses a prefix it doesn't know", () => {
    const s = session("3.3");
    s.buffer = "000055aa";
    expect(nextFrame({ crypto }, s)).toBeNull();
    s.buffer = "ff".repeat(16);
    expect(() => nextFrame({ crypto }, s)).toThrow("bad frame prefix, the stream is out of sync");
  });

  it("reads a frame that says it's longer than the buffer as not yet whole", () => {
    const s = session("3.3");
    s.buffer = "000055aa" + "00000001" + "00000009" + "00000010" + "00".repeat(4);
    expect(nextFrame({ crypto }, s)).toBeNull();
  });

  it("uses the Frame type's payload as the decoder's input", () => {
    const frame: Frame = { cmd: 8, payload: "" };
    expect(decode({ crypto }, session("3.4"), frame)).toBeNull();
  });
});
