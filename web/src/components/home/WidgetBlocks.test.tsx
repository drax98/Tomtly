import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RenderedWidget } from "@/api";
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
  it("draws a time input with its value and step", () => {
    render(<WidgetBlocks widget={widget} devices={[]} write={vi.fn()} onPressed={vi.fn()} />);
    const at = screen.getByLabelText("From") as HTMLInputElement;
    expect(at.type).toBe("time");
    expect(at.value).toBe("07:15");
    expect(at.step).toBe("900");
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
    fireEvent.change(within(dialog).getByLabelText("Wake"), { target: { value: "06:30" } });
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

  it("lays a two-column dialog's inputs side by side", async () => {
    const two: RenderedWidget = { ...widget, blocks: [{ ...widget.blocks![2], columns: 2 }] };
    render(<WidgetBlocks widget={two} devices={[]} write={vi.fn()} onPressed={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit program" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Wake").closest(".grid")?.className).toContain("grid-cols-2");
  });

  it("shows a viewer neither inputs nor the dialog's button", () => {
    render(<WidgetBlocks widget={widget} devices={[]} write={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Edit program" })).toBeNull();
    expect(screen.queryByLabelText("From")).toBeNull();
    expect(screen.getByText("Salon · 22.0 °C")).toBeTruthy();
  });
});
