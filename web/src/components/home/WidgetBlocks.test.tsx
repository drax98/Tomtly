import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { toast } from "sonner";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RenderedWidget, WidgetBlock } from "@/api";
import { mockService } from "@/test/helpers";
import { WidgetBlocks } from "./WidgetBlocks";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn(), info: vi.fn() } }));

const widget: RenderedWidget = {
  key: "script:rooms/rooms",
  title: "Rooms",
  from: "rooms",
  blocks: [
    { type: "text", text: "Salon · 22.0 °C" },
    { type: "time", name: "at", label: "From", value: "07:15", step: 15 },
    {
      type: "dialog", label: "Edit program", title: "Salon's program", text: "Saved on the thermostat.", action: "saveProgram", args: { room: "Salon" },
      blocks: [
        { type: "group", label: "Workdays" },
        { type: "time", name: "w1", label: "Wake", value: "06:00", step: 15 },
        { type: "number", name: "t1", label: "Wake temperature", value: 21, min: 9, max: 30, step: 0.5, unit: "°C" },
        { type: "rows", rows: [{ label: "Now", value: "Program" }] },
      ],
    },
  ],
};
const PRESS = "POST /api/widgets/script%3Arooms/rooms/actions/saveProgram";

afterEach(cleanup);

describe("WidgetBlocks, time and dialog", () => {
  it("draws a time as hour and minute selects, the minutes in its step", async () => {
    render(<WidgetBlocks widget={widget} devices={[]} write={vi.fn()} onPressed={vi.fn()} />);
    expect(screen.getByRole("combobox", { name: "From, hour" }).textContent).toBe("07");
    const minute = screen.getByRole("combobox", { name: "From, minute" });
    expect(minute.textContent).toBe("15");
    fireEvent.keyDown(minute, { key: "Enter" });
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["00", "15", "30", "45"]);
  });

  it("still shows a minute between the steps, as a device may hold (22:59 in 10-minute steps)", async () => {
    const odd: RenderedWidget = { ...widget, blocks: [{ type: "time", name: "at", label: "From", value: "22:59", step: 10 }] };
    render(<WidgetBlocks widget={odd} devices={[]} write={vi.fn()} onPressed={vi.fn()} />);
    const minute = screen.getByRole("combobox", { name: "From, minute" });
    expect(minute.textContent).toBe("59");
    fireEvent.keyDown(minute, { key: "Enter" });
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["00", "10", "20", "30", "40", "50", "59"]);
  });

  it("opens the form, and Save sends its values with the dialog's args, then closes and refreshes", async () => {
    const calls = mockService({ [PRESS]: () => ({ body: { live: true } }) });
    const onPressed = vi.fn();
    render(<WidgetBlocks widget={widget} devices={[]} write={vi.fn()} onPressed={onPressed} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit program" }));
    const dialog = await screen.findByRole("dialog", { name: "Salon's program" });
    expect(within(dialog).getByText("Saved on the thermostat.")).toBeTruthy();
    expect(within(dialog).getByRole("region", { name: "Workdays" })).toBeTruthy();
    expect(within(dialog).getByText("Program")).toBeTruthy();
    fireEvent.keyDown(within(dialog).getByRole("combobox", { name: "Wake, minute" }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "30" }));
    fireEvent.change(within(dialog).getByLabelText(/Wake temperature/), { target: { value: "21.5" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onPressed).toHaveBeenCalled());
    expect(calls.find((c) => c.key === PRESS)?.body).toEqual({ input: { w1: "06:30", t1: 21.5, room: "Salon" } });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("keeps the form open when the save fails, and Cancel closes it without sending", async () => {
    const calls = mockService({ [PRESS]: () => ({ status: 400, body: { error: "the thermostat didn't answer" } }) });
    render(<WidgetBlocks widget={widget} devices={[]} write={vi.fn()} onPressed={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit program" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((c) => c.key === PRESS)).toBe(true));
    expect(screen.getByRole("dialog")).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.filter((c) => c.key === PRESS)).toHaveLength(1);
  });

  it("shows a viewer neither inputs nor the dialog's button", () => {
    render(<WidgetBlocks widget={widget} devices={[]} write={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Edit program" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "From, hour" })).toBeNull();
    expect(screen.getByText("Salon · 22.0 °C")).toBeTruthy();
  });
});

const WEEK = [{ time: "06:00", temp: 21 }, { time: "08:00", temp: 18 }, { time: "16:00", temp: 21 }, { time: "22:00", temp: 17 }];
const schedule = (name: string, label: string, periods: WidgetBlock["periods"], now?: number): WidgetBlock => ({ type: "schedule", name, label, periods, now, min: 5, max: 30, step: 15 });
const mode: WidgetBlock = { type: "choice", name: "mode", label: "Mode", value: "Program", options: ["Program", "Manual"], action: "setMode", args: { room: "Salon" } };
const program = (...blocks: WidgetBlock[]): RenderedWidget => ({ ...widget, blocks: [{ type: "dialog", label: "Room program", action: "saveProgram", args: { room: "Salon" }, blocks }] });
const SET_MODE = "POST /api/widgets/script%3Arooms/rooms/actions/setMode";

async function open(w: RenderedWidget, onPressed = vi.fn()) {
  const view = render(<WidgetBlocks widget={w} devices={[]} write={vi.fn()} onPressed={onPressed} />);
  fireEvent.click(screen.getByRole("button", { name: "Room program" }));
  return { dialog: await screen.findByRole("dialog"), ...view };
}

async function pick(trigger: HTMLElement, option: string) {
  fireEvent.keyDown(trigger, { key: "Enter" });
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

describe("WidgetBlocks, schedule and choice", () => {
  it("draws a program's rows, its day bar and the row running now, on the tab that runs now", async () => {
    const { dialog } = await open(program(schedule("weekend", "Sat–Sun", [{}, {}]), schedule("week", "Mon–Fri", WEEK, 2)));
    expect(within(dialog).getByRole("tab", { name: "Mon–Fri" }).getAttribute("aria-selected")).toBe("true");
    expect(within(dialog).getAllByRole("group", { name: /^Period \d of 4$/ })).toHaveLength(4);
    expect(within(dialog).getByRole("combobox", { name: "Period 1 start, hour" }).textContent).toBe("06");
    expect(within(dialog).getByRole("combobox", { name: "Period 2 temperature, °C" }).textContent).toBe("18°");

    const segments = [...screen.getByTestId("day-bar").children] as HTMLElement[];
    // midnight to 06:00 still runs the last period, 17°
    expect(segments.map((s) => s.style.flexGrow)).toEqual(["360", "120", "480", "360", "120"]);
    expect(segments.map((s) => s.textContent)).toEqual(["17°", "21°", "18°", "21°", "17°"]);
    expect(segments.map((s) => s.className.includes("ring-2"))).toEqual([false, false, false, true, false]);

    const now = within(dialog).getByRole("group", { name: "Period 3 of 4" });
    expect(now.className).toContain("bg-primary/10");
    expect(within(now).getByText("running now")).toBeTruthy();
  });

  it("draws tabs only for two or more schedules, and an empty program as a box that says so", async () => {
    const { dialog } = await open(program(schedule("week", "Every day", [{}, {}])));
    expect(within(dialog).queryByRole("tablist")).toBeNull();
    expect(within(dialog).getByRole("heading", { name: "Every day" })).toBeTruthy();
    expect(within(dialog).getByText(/^Not read yet\./)).toBeTruthy();
  });

  it("Save sends each program's rows, or null for one left empty, and no choice", async () => {
    const calls = mockService({ [PRESS]: () => ({ body: { live: true } }) });
    const { dialog } = await open(program(mode, schedule("week", "Mon–Fri", WEEK, 0), schedule("weekend", "Sat–Sun", [{}, {}])));
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((c) => c.key === PRESS)).toBe(true));
    expect(calls.find((c) => c.key === PRESS)?.body).toEqual({ input: { week: WEEK, weekend: null, room: "Salon" } });
  });

  const bad = [{ time: "06:00", temp: 21 }, { time: "05:00", temp: 18 }];

  it("shows an order problem at once, but a program nobody edited doesn't block Save", async () => {
    const calls = mockService({ [PRESS]: () => ({ body: { live: true } }) });
    const { dialog } = await open(program(schedule("week", "Mon–Fri", bad, 0), schedule("weekend", "Sat–Sun", WEEK)));
    expect(within(dialog).getByRole("alert").textContent).toBe("Must start after 06:00");
    expect(within(dialog).getByRole("combobox", { name: "Period 2 start, hour" }).getAttribute("aria-describedby")).toBe(within(dialog).getByRole("alert").id);
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.filter((c) => c.key === PRESS)).toHaveLength(1));
  });

  it("an edited program's problem blocks Save, showing its tab and focusing the control", async () => {
    const calls = mockService({ [PRESS]: () => ({ body: { live: true } }) });
    const { dialog } = await open(program(schedule("week", "Mon–Fri", bad, 0), schedule("weekend", "Sat–Sun", WEEK)));
    await pick(within(dialog).getByRole("combobox", { name: "Period 1 temperature, °C" }), "22°");
    fireEvent.mouseDown(within(dialog).getByRole("tab", { name: "Sat–Sun" }));
    expect(within(dialog).getByRole("tab", { name: "Mon–Fri (1 problem)" })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(within(dialog).getByRole("tab", { name: "Mon–Fri" }).getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(within(dialog).getByRole("combobox", { name: "Period 2 start, hour" }));
    expect(calls.filter((c) => c.key === PRESS)).toHaveLength(0);
  });

  it("asks for a half-set row only after a Save attempt", async () => {
    mockService({});
    const { dialog } = await open(program(schedule("week", "Mon–Fri", [{}, {}])));
    await pick(within(dialog).getByRole("combobox", { name: "Period 1 temperature, °C" }), "20°");
    expect(within(dialog).queryByRole("alert")).toBeNull();
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(within(dialog).getAllByRole("alert").map((a) => a.textContent)).toEqual(["Set a time and temperature", "Set a time and temperature"]);
    expect(document.activeElement).toBe(within(dialog).getByRole("combobox", { name: "Period 1 start, hour" }));
  });

  it("a choice sends only its pick and args, keeps the dialog and its edits, and shows busy until drawn", async () => {
    const calls = mockService({ [SET_MODE]: () => ({ body: { live: true } }) });
    const onPressed = vi.fn();
    const w = program(mode, schedule("week", "Mon–Fri", WEEK, 0));
    const { dialog, rerender } = await open(w, onPressed);
    await pick(within(dialog).getByRole("combobox", { name: "Period 1 temperature, °C" }), "22°");
    const manual = within(dialog).getByRole("radio", { name: "Manual" });
    fireEvent.click(manual);
    await waitFor(() => expect(onPressed).toHaveBeenCalled());
    expect(calls.find((c) => c.key === SET_MODE)?.body).toEqual({ input: { mode: "Manual", room: "Salon" } });
    expect(manual.getAttribute("aria-busy")).toBe("true");

    const drawn = program({ ...mode, value: "Manual" }, schedule("week", "Mon–Fri", WEEK, 0));
    rerender(<WidgetBlocks widget={drawn} devices={[]} write={vi.fn()} onPressed={onPressed} />);
    expect(screen.getByRole("dialog")).toBeTruthy();
    await waitFor(() => expect(manual.getAttribute("aria-busy")).toBeNull());
    expect(manual.getAttribute("aria-checked")).toBe("true");
    expect(within(dialog).getByRole("combobox", { name: "Period 1 temperature, °C" }).textContent).toBe("22°");
  });

  it("a choice drops busy and says so when the script is practising", async () => {
    mockService({ [SET_MODE]: () => ({ body: { live: false } }) });
    const onPressed = vi.fn();
    render(<WidgetBlocks widget={{ ...widget, blocks: [mode] }} devices={[]} write={vi.fn()} onPressed={onPressed} />);
    const manual = screen.getByRole("radio", { name: "Manual" });
    fireEvent.click(manual);
    await waitFor(() => expect(toast.info).toHaveBeenCalled());
    expect(manual.getAttribute("aria-busy")).toBeNull();
    expect(screen.getByRole("radio", { name: "Program" }).getAttribute("aria-checked")).toBe("true");
    expect(onPressed).not.toHaveBeenCalled();
  });

  it("a choice reverts with an error when no redraw confirms it within 10 seconds", async () => {
    mockService({ [SET_MODE]: () => ({ body: { live: true } }) });
    const onPressed = vi.fn();
    render(<WidgetBlocks widget={{ ...widget, blocks: [mode] }} devices={[]} write={vi.fn()} onPressed={onPressed} />);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"], shouldAdvanceTime: true });
    try {
      fireEvent.click(screen.getByRole("radio", { name: "Manual" }));
      await waitFor(() => expect(onPressed).toHaveBeenCalled());
      act(() => vi.advanceTimersByTime(10_000));
    } finally {
      vi.useRealTimers();
    }
    expect(toast.error).toHaveBeenCalledWith("Mode didn't change", expect.anything());
    expect(screen.getByRole("radio", { name: "Program" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("radio", { name: "Manual" }).getAttribute("aria-busy")).toBeNull();
  });

  it("a dialog with nothing to fill in has only Close", async () => {
    const { dialog } = await open(program({ type: "text", text: "Window open" }, mode));
    expect(within(dialog).getByText("Close", { selector: "form button" })).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "Save" })).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "Cancel" })).toBeNull();
  });
});
