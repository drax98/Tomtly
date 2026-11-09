import { describe, expect, it } from "vitest";
import type { DataPoint, Device } from "@/api";
import { display, findDp, groupByKind, groupByRoom, hueToHex, label, matches, summarize } from "./device";

const device = (id: string, kind: string): Device => ({
  id, kind, adapter: "t", name: id, category: "", product: "", online: true, dps: [],
});
const titles = (groups: { title: string }[]) => groups.map((g) => g.title);

describe("groupByRoom", () => {
  it("keeps room order, drops empty rooms and puts the rest under Other", () => {
    const groups = groupByRoom([device("a", "light"), device("b", "light"), device("c", "light")], {
      Second: ["b"], Empty: ["gone"], First: ["a"],
    });
    expect(titles(groups)).toEqual(["Second", "First", "Other"]);
    expect(groups[2].devices.map((d) => d.id)).toEqual(["c"]);
  });

  it("shows everything under Other with no rooms configured", () => {
    expect(titles(groupByRoom([device("a", "light")], {}))).toEqual(["Other"]);
  });
});

describe("groupByKind", () => {
  it("gives a third-party kind its own group after the known ones", () => {
    const groups = groupByKind([device("a", "aquarium"), device("b", "climate"), device("c", "other")]);
    expect(titles(groups)).toEqual(["Climate", "Aquarium", "Other"]);
  });
});

describe("display", () => {
  const dp = (over: Partial<DataPoint>): DataPoint => ({
    dp: "1", code: "x", type: "Integer", values: {}, scale: 0, writable: false, value: null, ...over,
  });

  it("applies scale and unit", () => {
    expect(display(dp({ scale: 1, value: 205, values: { unit: "°C" } }))).toBe("20.5 °C");
  });

  it("marks unknown values rather than showing zero or off", () => {
    expect(display(dp({ value: null }))).toBe("–");
  });

  it("reads booleans and names", () => {
    expect(display(dp({ value: false }))).toBe("Off");
    expect(display(dp({ value: "charging" }))).toBe("Charging");
  });
});

describe("summarize and matches", () => {
  const dp = (code: string, value: boolean | number, scale = 0): DataPoint => ({ dp: code, code, type: "Boolean", values: {}, scale, writable: true, value });
  const lamp = { ...device("Lamp", "light"), dps: [dp("switch", true)] };
  const heater = { ...device("Heater", "climate"), dps: [dp("temp_current", 215, 1)] };
  const window = { ...device("Window", "sensor"), dps: [dp("open", true)] };

  it("reads a room in one line", () => {
    expect(summarize([lamp, heater, window, { ...lamp, online: false }])).toBe("1 of 1 on · 21.5°C · 1 open · 1 offline");
  });

  it("finds devices by name, room or type", () => {
    expect(matches(lamp, "Kitchen", "kitchen lamp")).toBe(true);
    expect(matches(lamp, "Kitchen", "plugs")).toBe(false);
    expect(matches(heater, "Hall", "Climate")).toBe(true);
  });

  it("uses plain words for yes/no readings", () => {
    expect(display(dp("open", true))).toBe("Open");
    expect(label("temp_set")).toBe("Target temperature");
  });
});

describe("devices that can't be read here", () => {
  it("aren't counted as offline", () => {
    const base = { adapter: "x", kind: "sensor", category: "", product: "", name: "T", online: false };
    const note = { dp: "u", code: "unavailable", type: "String", values: {}, scale: 0, writable: false, value: "Bluetooth" };
    expect(summarize([{ ...base, id: "a", dps: [note] }, { ...base, id: "b", dps: [] }])).toBe("1 offline");
  });
});

describe("device words", () => {
  it("doesn't show underscores or a bare on or off for a yes or no", () => {
    expect(display({ code: "relay_status", dp: "1", type: "Enum", values: {}, scale: 0, writable: false, value: "power_off" })).toBe("Power off");
    expect(display({ code: "no_water", dp: "1", type: "Boolean", values: {}, scale: 0, writable: false, value: false })).toBe("No");
    expect(label("c_f")).toBe("Temperature unit");
  });
});

describe("text from a device", () => {
  const text = (code: string, value: string): DataPoint => ({ dp: "1", code, type: "String", values: {}, scale: 0, writable: false, value });

  it("reads JSON as pairs rather than as a raw string", () => {
    expect(display(text("schedule", '{"day":"mon","hour":7}'))).toBe("Day: mon, Hour: 7");
  });

  it("cuts a long blob short, and leaves its letters alone", () => {
    const shown = display(text("ir_code", "aGVsbG8gd29ybGQ_aGVsbG8gd29ybGQ_aGVsbG8gd29ybGQ_aGVsbG8"));
    expect(shown.startsWith("aGVsbG8gd29ybGQ_")).toBe(true);
    expect(shown.endsWith("…")).toBe(true);
  });
});

describe("yes and no words by kind of reading", () => {
  const bool = (code: string, value: boolean): DataPoint => ({ dp: "1", code, type: "Boolean", values: {}, scale: 0, writable: false, value });

  it("says heating or idle, and open or closed, for what those readings mean", () => {
    expect(display(bool("heating_state", true))).toBe("Heating");
    expect(display(bool("heating_state", false))).toBe("Idle");
    expect(display(bool("window_state", true))).toBe("Open");
    expect(display(bool("doorcontact_state", false))).toBe("Closed");
  });

  it("says detected or clear for sensors, but only when the code starts with the sensor's name", () => {
    expect(display(bool("pir", true))).toBe("Detected");
    expect(display(bool("smoke_sensor_status", false))).toBe("Clear");
    expect(display(bool("co_status", true))).toBe("Detected");
    expect(display(bool("lamp_pir", true))).toBe("On");
  });

  it("says yes or no for the few flags that read that way", () => {
    expect(display(bool("temper_alarm", true))).toBe("Yes");
    expect(display(bool("pump_running", false))).toBe("No");
    expect(display(bool("reopen", true))).toBe("On");
  });
});

describe("display details", () => {
  const dp = (over: Partial<DataPoint>): DataPoint => ({ dp: "1", code: "x", type: "Integer", values: {}, scale: 0, writable: false, value: null, ...over });

  it("shows a number as it is when it has no scale or unit", () => {
    expect(display(dp({ value: 5 }))).toBe("5");
  });

  it("shows a missing value as a dash, even when the value is undefined rather than null", () => {
    expect(display(dp({ value: undefined as unknown as null }))).toBe("–");
  });

  it("reads JSON text as pairs and keeps nested values as JSON", () => {
    const text = (value: string) => dp({ type: "String", code: "schedule", value });
    expect(display(text('{"a":{"b":1}}'))).toBe('A: {"b":1}');
    expect(display(text("7"))).toBe("7");
  });

  it("cuts text at exactly 48 characters and says so with an ellipsis", () => {
    const text = (value: string) => dp({ type: "String", code: "note", value });
    const fits = "a".repeat(48);
    expect(display(text(fits))).toBe(fits);
    expect(display(text(`${fits}b`))).toBe(`${fits}…`);
  });
});

describe("findDp", () => {
  it("returns the first code the device has, and nothing when it has none", () => {
    const d = { ...device("x", "light"), dps: [{ dp: "2", code: "power", type: "Boolean", values: {}, scale: 0, writable: true, value: true }] };
    expect(findDp(d, "switch", "power")?.dp).toBe("2");
    expect(findDp(d, "switch")).toBeUndefined();
  });
});

describe("groupByKind extras", () => {
  it("puts each unknown kind in one group of its own, with its devices in it", () => {
    const groups = groupByKind([device("a", "smart_plug"), device("b", "aquarium"), device("c", "aquarium"), device("d", "smart_plug")]);
    expect(groups.map((g) => g.title)).toEqual(["Aquarium", "Smart plug"]);
    expect(groups[0].devices.map((d) => d.id)).toEqual(["b", "c"]);
    expect(groups[1].devices.map((d) => d.id)).toEqual(["a", "d"]);
  });

  it("only shows the groups that have devices", () => {
    expect(groupByKind([device("a", "cover")]).map((g) => g.title)).toEqual(["Blinds"]);
  });
});

describe("summarize readings", () => {
  const dp = (code: string, value: boolean | number, scale = 0): DataPoint => ({ dp: code, code, type: "Boolean", values: {}, scale, writable: true, value });
  const withDps = (...dps: DataPoint[]) => ({ ...device("d", "light"), dps });

  it("counts each kind of power switch as a light or plug that can be on", () => {
    for (const code of ["switch", "power", "switch_1", "switch_led"]) {
      expect(summarize([withDps(dp(code, true))])).toBe("1 of 1 on");
    }
  });

  it("counts a device as on only from its own power switch", () => {
    expect(summarize([withDps(dp("switch", false), dp("other", true))])).toBe("0 of 1 on");
    expect(summarize([withDps(dp("switch", true), dp("lock_flag", true))])).toBe("1 of 1 on");
  });

  it("averages the temperatures of devices that report one", () => {
    const a = { ...device("a", "climate"), dps: [dp("temp_current", 215, 1)] };
    const b = { ...device("b", "climate"), dps: [dp("temp_current", 195, 1)] };
    expect(summarize([a, b])).toBe("20.5°C");
  });

  it("leaves out a temperature that is not a number", () => {
    const a = { ...device("a", "climate"), dps: [dp("temp_current", true)] };
    expect(summarize([a])).toBe("");
  });

  it("says nothing about offline when every device is reachable", () => {
    expect(summarize([withDps(dp("switch", true))])).not.toContain("offline");
  });
});

describe("matches extras", () => {
  it("finds a device by its kind's name, as the page shows it", () => {
    expect(matches(device("Shade", "cover"), "Hall", "blinds")).toBe(true);
    expect(matches(device("Shade", "aquarium"), "Hall", "blinds")).toBe(false);
  });

  it("needs every word typed to match", () => {
    expect(matches(device("Lamp", "light"), "Hall", "  lamp   hall ")).toBe(true);
    expect(matches(device("Lamp", "light"), "Hall", "lamp garage")).toBe(false);
  });
});

describe("hueToHex", () => {
  it("draws a hue and saturation at full brightness", () => {
    expect(hueToHex(0, 1)).toBe("#ff0000");
    expect(hueToHex(120, 1)).toBe("#00ff00");
    expect(hueToHex(240, 0.5)).toBe("#8080ff");
    expect(hueToHex(220, 0)).toBe("#ffffff");
  });
});
