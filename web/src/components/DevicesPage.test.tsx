import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DataPoint, Device, Rooms } from "@/api";
import { WithRole } from "@/test/helpers";
import { DevicesPage } from "./DevicesPage";

const dp = (code: string, value: boolean | number | string | null, over: Partial<DataPoint> = {}): DataPoint => ({
  dp: "1", code, type: "Boolean", values: {}, scale: 0, writable: true, value, ...over,
});
const device = (id: string, name: string, kind: string, over: Partial<Device> = {}): Device => ({
  id, adapter: "tuya", name, kind, category: "", product: "", online: true, dps: [dp("switch_led", false)], ...over,
});

const lamp = device("lamp", "Lamp", "light");
const heater = device("heater", "Heater", "climate", { dps: [dp("temp_current", 215, { type: "Integer", scale: 1, writable: false, values: { unit: "°C" } })] });
const shade = device("shade", "Shade", "cover");
const rooms: Rooms = { Hall: ["lamp"], Kitchen: ["heater"] };

const show = (props: Partial<Parameters<typeof DevicesPage>[0]> = {}, role: "viewer" | "operator" = "operator") => {
  const write = vi.fn();
  const utils = render(
    <WithRole role={role}>
      <DevicesPage devices={[lamp, heater, shade]} rooms={rooms} reachable write={write} {...props} />
    </WithRole>,
  );
  return { ...utils, write };
};

beforeEach(() => localStorage.clear());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("DevicesPage grouping", () => {
  it("groups by room in the order set, with the rest under Other, and offers a jump to each", () => {
    show();
    const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
    expect(headings).toEqual(["Hall", "Kitchen", "Other"]);
    const jump = screen.getByRole("navigation", { name: "Jump to" });
    expect(within(jump).getByRole("link", { name: "Kitchen" }).getAttribute("href")).toBe("#group-kitchen");
  });

  it("scrolls to a group when its jump link is used", () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    show();
    fireEvent.click(within(screen.getByRole("navigation", { name: "Jump to" })).getByRole("link", { name: "Kitchen" }));
    expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
  });

  it("switches to grouping by type, remembers it, and groups by type", () => {
    const { unmount } = show();
    fireEvent.mouseDown(screen.getByRole("tab", { name: "By type" }));
    expect(localStorage.getItem("groupBy")).toBe("kind");
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Climate", "Lights", "Blinds"]);
    unmount();
    show();
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Climate", "Lights", "Blinds"]);
  });

  it("doesn't offer grouping by room when no rooms are set, and no jump list for one group", () => {
    show({ rooms: {} });
    expect(screen.queryByRole("tablist", { name: "Group devices" })).toBeNull();
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Climate", "Lights", "Blinds"]);
  });

  it("has no jump list for a single group", () => {
    show({ devices: [lamp] });
    expect(screen.queryByRole("navigation", { name: "Jump to" })).toBeNull();
    expect(screen.getByRole("tablist", { name: "Group devices" })).toBeTruthy();
  });
});

describe("DevicesPage search", () => {
  it("narrows to matching devices, and says when nothing matches with a way out", () => {
    show();
    fireEvent.change(screen.getByRole("searchbox", { name: "Find a device" }), { target: { value: "heat" } });
    expect(screen.queryByText("Lamp")).toBeNull();
    expect(screen.getByText("Heater")).toBeTruthy();
    fireEvent.change(screen.getByRole("searchbox", { name: "Find a device" }), { target: { value: "zzz" } });
    expect(screen.getByText('Nothing matches "zzz"')).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear the search" }));
    expect(screen.getByText("Lamp")).toBeTruthy();
  });

  it("hides the offline note while searching", () => {
    show({ devices: [lamp, { ...heater, online: false }, { ...shade, online: false }] });
    expect(screen.getByText(/2 devices are offline/)).toBeTruthy();
    fireEvent.change(screen.getByRole("searchbox", { name: "Find a device" }), { target: { value: "lamp" } });
    expect(screen.queryByText(/offline/)).toBeNull();
  });
});

describe("DevicesPage status", () => {
  it("counts offline devices in the singular and the plural", () => {
    const { unmount } = show({ devices: [lamp, { ...heater, online: false }] });
    expect(screen.getByText(/^1 device is offline/)).toBeTruthy();
    unmount();
    show({ devices: [{ ...lamp, online: false }, { ...heater, online: false }] });
    expect(screen.getByText(/^2 devices are offline/)).toBeTruthy();
  });

  it("warns that what's shown may be out of date when the service can't be reached", () => {
    show({ reachable: false, devices: [lamp] });
    expect(screen.getByText("Can't reach the home service")).toBeTruthy();
    expect(screen.getByText(/changes won't be sent/)).toBeTruthy();
  });

  it("shows a loading state until the first list arrives, and not while unreachable", () => {
    const { unmount } = show({ devices: null });
    expect(screen.getByLabelText("Loading devices")).toBeTruthy();
    unmount();
    show({ devices: null, reachable: false });
    expect(screen.queryByLabelText("Loading devices")).toBeNull();
  });

  it("says there are no devices yet, and what to do about it", () => {
    show({ devices: [] });
    expect(screen.getByText("No devices yet")).toBeTruthy();
    expect(screen.getByText(/Add one on the Integrations page/)).toBeTruthy();
  });

  it("shows the reason the last command to a device failed, on that device", () => {
    show({ devices: [{ ...lamp, error: "device offline" }, heater] });
    expect(screen.getByRole("alert").textContent).toBe("The last command failed: device offline");
  });
});

describe("DevicesPage access", () => {
  it("tells a viewer they can only look, and sends a change from an operator through", () => {
    const { write, unmount } = show({}, "viewer");
    expect(screen.getByText(/only change things with the Family role or higher/)).toBeTruthy();
    expect((screen.getByRole("switch", { name: "Lamp Light" }) as HTMLButtonElement).disabled).toBe(true);
    unmount();
    const operator = show({}, "operator");
    expect(screen.getByText(/Tap a switch to turn something on or off/)).toBeTruthy();
    fireEvent.click(screen.getByRole("switch", { name: "Lamp Light" }));
    expect(operator.write).toHaveBeenCalledWith("lamp", "1", true);
    expect(write).not.toHaveBeenCalled();
  });
});
