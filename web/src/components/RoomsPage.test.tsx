import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Device } from "@/api";
import { mockService, WithRole } from "@/test/helpers";
import { RoomsPage } from "./RoomsPage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const device = (id: string, name: string): Device => ({ id, adapter: "tuya", name, kind: "light", category: "", product: "", online: true, dps: [] });
const devices = [device("lamp", "Lamp"), device("kettle", "Kettle"), device("fan", "Fan")];
const rooms = { Hall: ["lamp"], Kitchen: ["kettle"] };

const show = (role: "admin" | "viewer" = "admin", routes = {}) => {
  const calls = mockService({
    "POST /api/rooms": () => ({ body: rooms }), "PATCH /api/rooms/Hall": () => ({ body: rooms }), "DELETE /api/rooms/Hall": () => ({ body: rooms }),
    "PATCH /api/devices/fan": () => ({ body: {} }), "PATCH /api/devices/lamp": () => ({ body: {} }), ...routes,
  });
  const onChanged = vi.fn();
  render(<WithRole role={role}><RoomsPage devices={devices} rooms={rooms} onChanged={onChanged} /></WithRole>);
  return { calls, onChanged };
};
const sent = (calls: { key: string; body: unknown }[], key: string) => calls.filter((c) => c.key === key).map((c) => c.body);

afterEach(cleanup);

describe("RoomsPage", () => {
  it("lists rooms in order with their devices, and what's in no room", () => {
    show();
    expect(screen.getAllByTestId(/^room-/).map((c) => c.dataset.testid)).toEqual(["room-Hall", "room-Kitchen"]);
    expect(within(screen.getByTestId("room-Hall")).getByText("1 device")).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "Not in a room" })).getByText("Fan")).toBeTruthy();
  });

  it("adds, renames, moves and deletes", async () => {
    const { calls, onChanged } = show();
    fireEvent.change(screen.getByLabelText("New room's name"), { target: { value: " Loft " } });
    fireEvent.click(screen.getByRole("button", { name: "Add room" }));
    await waitFor(() => expect(sent(calls, "POST /api/rooms")).toEqual([{ name: "Loft" }]));

    const hall = within(screen.getByTestId("room-Hall"));
    fireEvent.click(hall.getByRole("button", { name: "Rename" }));
    fireEvent.change(hall.getByLabelText("New name for Hall"), { target: { value: "Entry" } });
    fireEvent.click(hall.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(calls, "PATCH /api/rooms/Hall")).toContainEqual({ name: "Entry" }));

    fireEvent.click(hall.getByRole("button", { name: "Delete" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(calls.some((c) => c.key === "DELETE /api/rooms/Hall")).toBe(true));
    expect(onChanged).toHaveBeenCalled();
  });

  it("moves a device here by ticking it, and out to no room by unticking", async () => {
    const { calls } = show();
    fireEvent.click(within(screen.getByTestId("room-Hall")).getByRole("button", { name: "Choose devices" }));
    const dialog = within(await screen.findByRole("dialog", { name: "Devices in Hall" }));
    expect(dialog.getByText("in Kitchen")).toBeTruthy();
    fireEvent.click(dialog.getByRole("checkbox", { name: /Fan/ }));
    await waitFor(() => expect(sent(calls, "PATCH /api/devices/fan")).toEqual([{ room: "Hall" }]));
    fireEvent.click(dialog.getByRole("checkbox", { name: /Lamp/ }));
    await waitFor(() => expect(sent(calls, "PATCH /api/devices/lamp")).toEqual([{ room: "" }]));
  });

  it("shows a viewer the rooms and nothing to change them with", () => {
    show("viewer");
    expect(screen.getByTestId("room-Hall")).toBeTruthy();
    expect(screen.queryByLabelText("New room's name")).toBeNull();
    expect(screen.queryByRole("button", { name: /Rename|Delete|Choose devices/ })).toBeNull();
  });

  it("moves a room with the arrow keys on its handle, and says where it went", async () => {
    const { calls } = show();
    const handle = screen.getByRole("button", { name: "Move Hall" });
    fireEvent.keyDown(handle, { key: "ArrowUp" }); // already first: nothing to do
    fireEvent.keyDown(handle, { key: "ArrowDown" });
    await waitFor(() => expect(sent(calls, "PATCH /api/rooms/Hall")).toEqual([{ position: 1 }]));
    expect(screen.getByText("Hall moved to place 2 of 2.")).toBeTruthy();
  });

  it("drags a card onto another's place, and only saves when it really moved", async () => {
    const { calls } = show();
    const rect = (top: number) => ({ left: 0, right: 300, top, bottom: top + 100, width: 300, height: 100, x: 0, y: top, toJSON: () => ({}) }) as DOMRect;
    const hall = screen.getByTestId("room-Hall"), kitchen = screen.getByTestId("room-Kitchen");
    hall.getBoundingClientRect = () => rect(0);
    kitchen.getBoundingClientRect = () => rect(200);

    fireEvent.pointerDown(within(hall).getByText("Lamp"), { button: 0, pointerType: "mouse" });
    fireEvent.pointerMove(window, { clientX: 50, clientY: 50 }); // still over itself
    fireEvent.pointerUp(window);
    expect(sent(calls, "PATCH /api/rooms/Hall")).toEqual([]);

    fireEvent.pointerDown(within(hall).getByText("Lamp"), { button: 0, pointerType: "mouse" });
    fireEvent.pointerMove(window, { clientX: 50, clientY: 250 });
    expect(screen.getAllByTestId(/^room-/).map((c) => c.dataset.testid)).toEqual(["room-Kitchen", "room-Hall"]); // shown at once
    fireEvent.pointerUp(window);
    await waitFor(() => expect(sent(calls, "PATCH /api/rooms/Hall")).toEqual([{ position: 1 }]));
  });

  it("only lets a finger drag by the handle, so the page still scrolls", () => {
    const { calls } = show();
    fireEvent.pointerDown(within(screen.getByTestId("room-Hall")).getByText("Lamp"), { button: 0, pointerType: "touch" });
    fireEvent.pointerUp(window);
    expect(screen.getByTestId("room-Hall").className).not.toContain("ring-2");
    expect(sent(calls, "PATCH /api/rooms/Hall")).toEqual([]);
  });
});
