// A light's colour_data, as the three numbers the page and scripts can read and write. The host only writes Boolean, Enum,
// Integer and Action, so colour_hue, colour_saturation and colour_brightness stand in, and a write is rebuilt in exactly
// the form the device sent. Forms: Tuya's dj instruction set (JSON, https://developer.tuya.com/en/docs/iot/dj?id=K9i5ql3v98hn3)
// and TinyTuya's BulbDevice (rgb8 rrggbbhhhhssvv, hsv16 hhhhssssvvvv).
export interface Colour {
  form: "object" | "json" | "rgb8" | "hsv16";
  h: number;
  s: number;
  v: number;
}

export const COLOUR_PARTS: Record<string, "h" | "s" | "v"> = { colour_hue: "h", colour_saturation: "s", colour_brightness: "v" };

export function isColourCode(code: string): boolean {
  return /^colou?r_data(_v2|_hsv)?$/.test(code);
}

export function parseColour(value: unknown): Colour | null {
  let form: Colour["form"] = "object";
  let parsed = value as any;
  if (typeof value === "string") {
    if (/^[0-9a-f]{12}$/i.test(value)) return { form: "hsv16", h: hexAt(value, 0, 4), s: hexAt(value, 4, 4), v: hexAt(value, 8, 4) };
    if (/^[0-9a-f]{14}$/i.test(value)) return { form: "rgb8", h: hexAt(value, 6, 4), s: hexAt(value, 10, 2), v: hexAt(value, 12, 2) };
    form = "json";
    try {
      parsed = JSON.parse(value);
    } catch {
      return null;
    }
  }
  if (parsed === null || typeof parsed !== "object" || ![parsed.h, parsed.s, parsed.v].every((n) => typeof n === "number")) return null;
  return { form, h: parsed.h, s: parsed.s, v: parsed.v };
}

function hexAt(text: string, at: number, size: number): number {
  return parseInt(text.slice(at, at + size), 16);
}

function hex(n: number, size: number): string {
  return Math.round(n).toString(16).padStart(size, "0");
}

/** The top of saturation and brightness: the mapping's, else colour_data in JSON and rgb8 use 255, and the rest 1000. */
export function colourMax(c: Colour, code: string, values: Record<string, any>): number {
  const stated = values.s?.max;
  if (typeof stated === "number" && stated > 0) return stated;
  return c.form === "rgb8" || (code === "colour_data" && c.form !== "hsv16") ? 255 : 1000;
}

function rgb(c: Colour, max: number): string {
  const [s, v] = [c.s / max, c.v / max];
  const channel = (n: number): string => {
    const k = (n + c.h / 60) % 6;
    return hex(255 * (v - v * s * Math.max(0, Math.min(k, 4 - k, 1))), 2);
  };
  return channel(5) + channel(3) + channel(1);
}

/** What goes back to the device, in the form it came. */
export function formatColour(c: Colour): unknown {
  switch (c.form) {
    case "object": return { h: c.h, s: c.s, v: c.v };
    case "json": return JSON.stringify({ h: c.h, s: c.s, v: c.v });
    case "hsv16": return hex(c.h, 4) + hex(c.s, 4) + hex(c.v, 4);
    case "rgb8": return rgb(c, 255) + hex(c.h, 4) + hex(c.s, 2) + hex(c.v, 2);
  }
}

export function colourWords(c: Colour, max: number): string {
  const percent = (n: number) => Math.round(n / max * 1000) / 10;
  return `hue ${c.h}, saturation ${percent(c.s)}%, brightness ${percent(c.v)}%`;
}
