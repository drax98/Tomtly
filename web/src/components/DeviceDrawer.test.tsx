import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DataPoint, Device } from "@/api";
import { mockService, WithRole } from "@/test/helpers";
import { DevicesPage } from "./DevicesPage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const dp = (n: string, code: string, value: boolean | number | string | null, over: Partial<DataPoint> = {}): DataPoint => ({
  dp: n, code, type: "Boolean", values: {}, scale: 0, writable: true, value, ...over,
});
const plug: Device = {
  id: "tuya:plug1", adapter: "tuya", name: "Desk plug", kind: "plug", category: "cz", product: "Smart Plug", online: true,
  dps: [dp("1", "switch_1", true), dp("2", "cur_power", 12, { type: "Integer", writable: false, values: { unit: "W" } }), dp("3", "child_lock", false), dp("4", "countdown_1", 0, { type: "Integer", values: { min: 0, max: 86400 } })],
};

const show = (role: "operator" | "viewer" | "admin" = "operator", pins: Record<string, string[]> = {}, put = vi.fn(() => ({ body: {} })), patch = vi.fn(() => ({ body: {} }))) => {
  const calls = mockService({ "GET /api/pins": () => ({ body: pins }), "PUT /api/devices/tuya%3Aplug1/pins": put, "PATCH /api/devices/tuya%3Aplug1": patch });
  const write = vi.fn();
  const onEdited = vi.fn();
  render(<WithRole role={role}><DevicesPage devices={[plug]} rooms={{ Office: ["tuya:plug1"], Attic: [] }} reachable write={write} onEdited={onEdited} /></WithRole>);
  return { calls, write, put, patch, onEdited };
};
// hidden: while the drawer is open, the page behind it is hidden from assistive tech, as it should be.
const card = () => screen.getByRole("button", { name: "Details of Desk plug", hidden: true }).closest(".rounded-xl") as HTMLElement;

afterEach(cleanup);

describe("Device drawer", () => {
  it("opens from anywhere on the card that isn't a control, with what the device is and where", async () => {
    const { write } = show();
    fireEvent.click(within(card()).getByRole("switch")); // a control does its own thing
    expect(write).toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(card());
    const drawer = within(await screen.findByRole("dialog", { name: "Desk plug" }));
    expect(drawer.getByText("tuya:plug1")).toBeTruthy();
    expect(drawer.getByText("Office")).toBeTruthy();
    expect(drawer.getByText("tuya")).toBeTruthy();
    expect(drawer.getAllByText("Smart Plug").length).toBeGreaterThan(0);
    for (const code of ["switch_1", "cur_power", "child_lock", "countdown_1"]) expect(drawer.getByText(code)).toBeTruthy();
  });

  it("doesn't open when an option is chosen from a menu on the card", async () => {
    const fan: Device = { ...plug, id: "fan", name: "Fan", kind: "fan", dps: [dp("1", "mode", "low", { type: "Enum", values: { range: ["low", "high"] } })] };
    mockService({ "GET /api/pins": () => ({ body: {} }) });
    const write = vi.fn();
    render(<WithRole role="operator"><DevicesPage devices={[fan]} rooms={{}} reachable write={write} /></WithRole>);
    fireEvent.keyDown(screen.getByRole("combobox", { name: "Fan Mode" }), { key: "Enter" });
    const option = await screen.findByRole("option", { name: "High" });
    fireEvent.click(option);
    await waitFor(() => expect(write).toHaveBeenCalledWith("fan", "1", "high"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("pins a value to the card, saves it for the whole home, and unpins it", async () => {
    const { put } = show();
    expect(card().querySelector("[data-testid=pinned]")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Details of Desk plug" }));
    const drawer = within(await screen.findByRole("dialog"));
    expect(drawer.getAllByRole("img", { name: "Always on the card" }).length).toBeGreaterThan(0); // its power switch is always there
    fireEvent.click(drawer.getByRole("button", { name: "Pin Countdown 1 to the card" }));
    await waitFor(() => expect(put).toHaveBeenCalledWith({ dps: ["4"] }, expect.anything()));
    expect(card().querySelector("[data-testid=pinned]")?.textContent).toContain("Countdown 1");
    fireEvent.click(drawer.getByRole("button", { name: "Pin Child lock to the card" }));
    await waitFor(() => expect(put).toHaveBeenLastCalledWith({ dps: ["4", "3"] }, expect.anything()));
    fireEvent.click(drawer.getByRole("button", { name: "Unpin Countdown 1 from the card" }));
    await waitFor(() => expect(put).toHaveBeenLastCalledWith({ dps: ["3"] }, expect.anything()));
  });

  it("puts a pin back when it can't be saved", async () => {
    show("operator", {}, vi.fn(() => ({ status: 503, body: { error: "the pins weren't saved. Try again." } })));
    fireEvent.click(screen.getByRole("button", { name: "Details of Desk plug" }));
    fireEvent.click(await screen.findByRole("button", { name: "Pin Child lock to the card" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Pin Child lock to the card" })).toBeTruthy());
    expect(card().querySelector("[data-testid=pinned]")).toBeNull();
  });

  it("shows a viewer the pins, but not a way to change them", async () => {
    show("viewer", { "tuya:plug1": ["3"] });
    await waitFor(() => expect(card().querySelector("[data-testid=pinned]")?.textContent).toContain("Child lock"));
    fireEvent.click(screen.getByRole("button", { name: "Details of Desk plug" }));
    const drawer = within(await screen.findByRole("dialog"));
    expect(drawer.queryByRole("button", { name: /^Pin / })).toBeNull();
    expect(drawer.getByLabelText("Pinned")).toBeTruthy();
  });

  it("lets an administrator rename it and move it to a room", async () => {
    const { patch, onEdited } = show("admin");
    fireEvent.click(screen.getByRole("button", { name: "Details of Desk plug" }));
    const drawer = within(await screen.findByRole("dialog"));
    fireEvent.click(drawer.getByRole("button", { name: "Edit" }));
    fireEvent.change(drawer.getByLabelText("Name"), { target: { value: "  Desk  " } });
    fireEvent.keyDown(drawer.getByRole("combobox", { name: "Room" }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "Attic" }));
    fireEvent.click(drawer.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patch).toHaveBeenCalledWith({ name: "Desk", room: "Attic" }, expect.anything()));
    await waitFor(() => expect(onEdited).toHaveBeenCalled());
  });

  it("sends the integration's own name as none, a new room by its name, and no room as empty", async () => {
    const { patch } = show("admin");
    fireEvent.click(screen.getByRole("button", { name: "Details of Desk plug" }));
    const drawer = within(await screen.findByRole("dialog"));
    fireEvent.click(drawer.getByRole("button", { name: "Edit" }));
    fireEvent.keyDown(drawer.getByRole("combobox", { name: "Room" }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "A new room…" }));
    fireEvent.click(drawer.getByRole("button", { name: "Save" }));
    expect(await drawer.findByRole("alert")).toBeTruthy(); // the new room needs a name
    fireEvent.change(drawer.getByLabelText("New room's name"), { target: { value: "Loft" } });
    fireEvent.click(drawer.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patch).toHaveBeenCalledWith({ name: "", room: "Loft" }, expect.anything()));
  });

  it("offers no editing to someone who isn't an administrator", async () => {
    show("operator");
    fireEvent.click(screen.getByRole("button", { name: "Details of Desk plug" }));
    const drawer = within(await screen.findByRole("dialog"));
    expect(drawer.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(drawer.getByText("An administrator can rename it or move it to another room.")).toBeTruthy();
  });
});
