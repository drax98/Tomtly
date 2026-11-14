import { describe, expect, it } from "vitest";
import { addedAfter, hourAfter, scheduleProblems, sentRows, sorted, type Row } from "./Schedule";

describe("scheduleProblems", () => {
  it("finds nothing wrong with an empty program or a complete one in order", () => {
    expect(scheduleProblems([{ time: "" }, { time: "" }])).toEqual([]);
    expect(scheduleProblems([{ time: "06:00", temp: 21 }, { time: "22:00", temp: 17 }])).toEqual([]);
  });

  it("finds two periods that start at once, and a device's program out of order, now", () => {
    expect(scheduleProblems([{ time: "06:00", temp: 21 }, { time: "06:00", temp: 18 }, { time: "05:00", temp: 17 }])).toEqual([
      { row: 1, text: "Two periods start at 06:00", onSave: false },
      { row: 2, text: "Must start after 06:00", onSave: false },
    ]);
  });

  it("wants every row set once one is, on Save", () => {
    expect(scheduleProblems([{ time: "06:00" }, { time: "" }, { time: "22:00", temp: 17 }])).toEqual([
      { row: 0, text: "Set a time and temperature", onSave: true },
      { row: 1, text: "Set a time and temperature", onSave: true },
    ]);
  });

  it("sends null for an empty program", () => {
    expect(sentRows([{ time: "" }])).toBeNull();
    expect(sentRows([{ time: "06:00", temp: 21 }, { time: "" }])).toEqual([{ time: "06:00", temp: 21 }, { time: null, temp: null }]);
  });

  it("works on any number of rows", () => {
    expect(scheduleProblems([{ time: "06:00", temp: 21 }])).toEqual([]);
    const five = ["01:00", "02:00", "03:00", "04:00", "03:30"].map((time) => ({ time, temp: 20 }));
    expect(scheduleProblems(five)).toEqual([{ row: 4, text: "Must start after 04:00", onSave: false }]);
  });
});

describe("adding a period", () => {
  it("starts an hour after the row before, capped at 23:50, or unset when that isn't after it", () => {
    expect(hourAfter("06:15")).toBe("07:15");
    expect(hourAfter("23:20")).toBe("23:50");
    expect(hourAfter("23:50")).toBe("");
    expect(hourAfter("")).toBe("");
  });

  it("goes after the given row with its temperature", () => {
    const out = addedAfter([{ time: "06:00", temp: 21 }, { time: "22:00", temp: 17 }], 0);
    expect(out.map(({ time, temp }) => ({ time, temp }))).toEqual([{ time: "06:00", temp: 21 }, { time: "07:00", temp: 21 }, { time: "22:00", temp: 17 }]);
    expect(out[1].key).toMatch(/^a\d+$/);
  });
});

describe("sorting a row by its start", () => {
  const rows = (...times: string[]): Row[] => times.map((time, i) => ({ time, temp: 20, key: `s${i}` }));
  const keys = (r: Row[]) => r.map((x) => x.key);

  it("moves a row that's out of order to its place", () => {
    expect(keys(sorted(rows("06:00", "08:00", "05:00"), 2))).toEqual(["s2", "s0", "s1"]);
    expect(keys(sorted(rows("09:00", "08:00", "22:00"), 0))).toEqual(["s1", "s0", "s2"]);
    expect(keys(sorted(rows("06:00", "", "05:00"), 2))).toEqual(["s2", "s0", "s1"]);
  });

  it("leaves a row in order, or without a time, where it is", () => {
    expect(keys(sorted(rows("06:00", "", "07:00", ""), 3))).toEqual(["s0", "s1", "s2", "s3"]);
    expect(keys(sorted(rows("06:00", "", "22:00"), 1))).toEqual(["s0", "s1", "s2"]);
    expect(keys(sorted(rows("06:00", "10:00", "22:00"), 1))).toEqual(["s0", "s1", "s2"]);
  });

  it("puts a row with the same start as another after it, which is a problem sorting can't fix", () => {
    const out = sorted(rows("08:00", "06:00", "08:00"), 0);
    expect(keys(out)).toEqual(["s1", "s2", "s0"]);
    expect(scheduleProblems(out)).toEqual([{ row: 2, text: "Two periods start at 08:00", onSave: false }]);
  });
});
