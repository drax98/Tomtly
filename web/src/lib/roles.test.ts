import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ago, rolesUpTo } from "./roles";

describe("rolesUpTo", () => {
  it("lets someone hand out their own role and the ones below it", () => {
    expect(rolesUpTo("viewer")).toEqual(["viewer"]);
    expect(rolesUpTo("editor")).toEqual(["viewer", "operator", "editor"]);
    expect(rolesUpTo("admin")).toEqual(["viewer", "operator", "editor", "admin"]);
  });
});

describe("ago", () => {
  const NOW = new Date("2026-11-01T12:00:00Z").getTime();
  const minutesAgo = (m: number) => new Date(NOW - m * 60000).toISOString();

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => vi.useRealTimers());

  it("says never for no time, and just now under a minute", () => {
    expect(ago(null)).toBe("Never");
    expect(ago(undefined)).toBe("Never");
    expect(ago("")).toBe("Never");
    expect(ago(minutesAgo(0.4))).toBe("Just now");
  });

  it("counts minutes up to the hour, from exactly one minute", () => {
    expect(ago(minutesAgo(1))).toBe("1 min ago");
    expect(ago(minutesAgo(59))).toBe("59 min ago");
  });

  it("counts hours from exactly an hour up to a day", () => {
    expect(ago(minutesAgo(60))).toBe("1 h ago");
    expect(ago(minutesAgo(120))).toBe("2 h ago");
    expect(ago(minutesAgo(90))).toBe("2 h ago");
    expect(ago(minutesAgo(1439))).toBe("24 h ago");
  });

  it("shows the date from a day on", () => {
    const at = minutesAgo(1440);
    expect(ago(at)).toBe(new Date(at).toLocaleDateString([], { day: "numeric", month: "short", year: "numeric" }));
  });
});
