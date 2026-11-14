import { describe, expect, it } from "vitest";
import { scheduleProblems, sentRows } from "./Schedule";

describe("scheduleProblems", () => {
  it("finds nothing wrong with an empty program or a complete one in order", () => {
    expect(scheduleProblems([{ time: "" }, { time: "" }])).toEqual([]);
    expect(scheduleProblems([{ time: "06:00", temp: 21 }, { time: "22:00", temp: 17 }])).toEqual([]);
  });

  it("wants every start after the one before, now", () => {
    expect(scheduleProblems([{ time: "06:00", temp: 21 }, { time: "06:00", temp: 18 }, { time: "05:00", temp: 17 }])).toEqual([
      { row: 1, text: "Must start after 06:00", onSave: false },
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
});
