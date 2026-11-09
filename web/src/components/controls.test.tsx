import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DataPoint, Device } from "@/api";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ActionButton, Control, DeviceCard, READ_ONLY_REASON, WriteAccess } from "./controls";

const dp = (code: string, over: Partial<DataPoint> = {}): DataPoint => ({
  dp: "1", code, type: "Boolean", values: {}, scale: 0, writable: true, value: null, ...over,
});
const device = (over: Partial<Device> = {}): Device => ({
  id: "d1", adapter: "t", name: "Lamp", kind: "light", category: "", product: "", online: true, dps: [], ...over,
});

const asWriter = (ui: ReactNode, canWrite = true) => (
  <WriteAccess.Provider value={canWrite}><TooltipProvider>{ui}</TooltipProvider></WriteAccess.Provider>
);

afterEach(cleanup);
beforeEach(() => vi.useRealTimers());

describe("DeviceCard", () => {
  it("says an offline device is offline, and shows what the integration said when it has a reason", () => {
    const { unmount } = render(asWriter(<DeviceCard device={device({ online: false })} />));
    expect(screen.getByText("Offline. Check it has power and Wi-Fi.")).toBeTruthy();
    unmount();
    render(asWriter(<DeviceCard device={device({ online: false, dps: [dp("unavailable", { code: "unavailable", type: "String", value: "Needs the hub" })] })} />));
    expect(screen.getByText("Needs the hub")).toBeTruthy();
    expect(screen.queryByText("Offline. Check it has power and Wi-Fi.")).toBeNull();
  });

  it("shows its children only when there are some", () => {
    const { unmount } = render(asWriter(<DeviceCard device={device()} />));
    expect(screen.queryByText("Inside")).toBeNull();
    unmount();
    render(asWriter(<DeviceCard device={device()}><p>Inside</p></DeviceCard>));
    expect(screen.getByText("Inside")).toBeTruthy();
  });

  it("explains, when hovered or focused, why a viewer's card can't be changed", async () => {
    render(asWriter(<DeviceCard device={device()}><p>Inside</p></DeviceCard>, false));
    const card = screen.getByText("Lamp").closest("[tabindex]") as HTMLElement;
    expect(card).toBeTruthy();
    fireEvent.focus(card);
    expect(await screen.findByText(READ_ONLY_REASON)).toBeTruthy();
  });
});

describe("ActionButton", () => {
  it("sends the action, says Sent for a moment, then goes back", () => {
    vi.useFakeTimers();
    const onSend = vi.fn();
    render(asWriter(<ActionButton text="Dock" onSend={onSend} />));
    fireEvent.click(screen.getByRole("button", { name: "Dock" }));
    expect(onSend).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Dock" }).textContent).toBe("Sent");
    act(() => vi.advanceTimersByTime(1499));
    expect(screen.getByRole("button", { name: "Dock" }).textContent).toBe("Sent");
    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByRole("button", { name: "Dock" }).textContent).toBe("Dock");
    vi.useRealTimers();
  });

  it("can't be pressed without write access", () => {
    const onSend = vi.fn();
    render(asWriter(<ActionButton text="Dock" onSend={onSend} />, false));
    expect((screen.getByRole("button", { name: "Dock" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("Control", () => {
  it("shows an action as a button for each thing it can do", () => {
    const write = vi.fn();
    const command = dp("command", { type: "Action", values: { range: ["start", "dock"] } });
    render(asWriter(<Control device={device({ kind: "vacuum" })} dp={command} write={write} />));
    fireEvent.click(screen.getByRole("button", { name: "Dock" }));
    expect(write).toHaveBeenCalledWith("d1", "1", "dock", false);
    expect(screen.getByRole("button", { name: "Start" })).toBeTruthy();
  });

  it("shows a reading that can't be changed as text, with long text in full on hover", () => {
    const long = "x".repeat(60);
    render(asWriter(<Control device={device()} dp={dp("note", { type: "String", writable: false, value: long })} write={vi.fn()} />));
    expect(screen.getByText(long.slice(0, 48) + "…")).toBeTruthy();
    expect(screen.getByTitle(long)).toBeTruthy();
  });

  it("turns a writable on-off value into a switch that sends the new state", () => {
    const write = vi.fn();
    render(asWriter(<Control device={device()} dp={dp("switch_led", { value: false })} write={write} />));
    fireEvent.click(screen.getByRole("switch", { name: "Lamp Light" }));
    expect(write).toHaveBeenCalledWith("d1", "1", true);
  });

  it("lets someone choose one of an enum's values and sends that", async () => {
    const write = vi.fn();
    const mode = dp("work_mode", { type: "Enum", value: "eco", values: { range: ["eco", "fast"] } });
    render(asWriter(<Control device={device()} dp={mode} write={write} />));
    fireEvent.keyDown(screen.getByRole("combobox", { name: "Lamp Light mode" }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "Fast" }));
    expect(write).toHaveBeenCalledWith("d1", "1", "fast");
  });

  it("shows a number with a range as a slider that sends where it was let go", () => {
    const write = vi.fn();
    const level = dp("bright_value", { type: "Integer", value: 40, scale: 0, values: { min: 0, max: 100, step: 5, unit: "%" } });
    render(asWriter(<Control device={device()} dp={level} write={write} />));
    expect(screen.getByText("40 %")).toBeTruthy();
    const thumb = screen.getByRole("slider", { name: "Lamp Brightness" });
    expect(thumb.getAttribute("aria-valuenow")).toBe("40");
    fireEvent.keyDown(thumb, { key: "ArrowRight" });
    expect(write).toHaveBeenCalledWith("d1", "1", 45);
  });

  it("shows a value it has no control for as plain text", () => {
    render(asWriter(<Control device={device()} dp={dp("temp_current", { type: "Integer", writable: true, value: 215, scale: 1, values: { unit: "°C" } })} write={vi.fn()} />));
    expect(screen.getByText("21.5 °C")).toBeTruthy();
  });

  it("can't change a control without write access", () => {
    render(asWriter(<Control device={device()} dp={dp("switch_led", { value: true })} write={vi.fn()} />, false));
    expect((screen.getByRole("switch", { name: "Lamp Light" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("ColourPicker", () => {
  const lamp = device({ dps: [
    dp("colour_hue", { dp: "h", type: "Integer", values: { min: 0, max: 360 }, value: 0 }),
    dp("colour_saturation", { dp: "s", type: "Integer", values: { min: 0, max: 1000 }, value: 500 }),
  ] });

  it("shows the light's colour, and writes the hue before the saturation once one is picked", async () => {
    const write = vi.fn();
    render(asWriter(<Control device={lamp} dp={lamp.dps![0]} write={write} />));
    const swatch = screen.getByRole("button", { name: "Lamp colour" });
    expect(swatch.style.background).toBe("rgb(255, 128, 128)");
    fireEvent.click(swatch);
    fireEvent.keyDown(await screen.findByRole("slider", { name: "Lamp colour" }), { key: "ArrowLeft" });
    await vi.waitFor(() => expect(write.mock.calls).toEqual([["d1", "h", 355], ["d1", "s", 500]]));
  });
});
