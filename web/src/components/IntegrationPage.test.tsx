import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Discovery, Integration } from "@/api";
import { ConfirmProvider } from "@/components/ConfirmDialog";
import { mockService, WithRole } from "@/test/helpers";
import { IntegrationPage } from "./IntegrationPage";

const lamp = { id: "x:lamp", name: "Lamp", kind: "light", online: true };
const integration: Integration = { type: "x", name: "X", devices: [lamp] };
const found: Discovery = {
  supported: true,
  devices: [
    { id: "x:lamp", name: "Lamp", state: "added", seen: true },
    { id: "x:fan", name: "Fan", state: "removed", seen: false },
    { id: "x:new", name: "Unknown device", address: "10.0.0.9", note: "Fetch the file again", state: "new", seen: true },
  ],
};

function show(role: "admin" | "viewer", calls: string[] = []) {
  mockService({
    "GET /api/integrations": () => ({ body: [integration] }),
    "POST /api/integrations/x/discover": () => { calls.push("scan"); return { body: found }; },
    "DELETE /api/devices/x%3Alamp": () => { calls.push("remove"); return { body: { ok: true } }; },
    "POST /api/devices/x%3Afan/restore": () => { calls.push("restore"); return { body: { ok: true } }; },
  });
  render(<WithRole role={role}><ConfirmProvider><IntegrationPage id="x" back={vi.fn()} /></ConfirmProvider></WithRole>);
}

afterEach(cleanup);

describe("IntegrationPage", () => {
  it("scans when it's opened, and offers back only what was removed", async () => {
    const calls: string[] = [];
    show("admin", calls);
    const fan = (await screen.findByText("Fan")).closest("li")!;
    expect(calls).toContain("scan");
    expect(within(fan).getByText(/not seen on the network/)).toBeTruthy();
    const stranger = screen.getByText("Unknown device").closest("li")!;
    expect(within(stranger).getByText("Fetch the file again")).toBeTruthy();
    expect(within(stranger).queryByRole("button")).toBeNull();
    fireEvent.click(within(fan).getByRole("button", { name: "Add back" }));
    await waitFor(() => expect(calls).toContain("restore"));
  });

  it("asks before removing, and says what it means", async () => {
    const calls: string[] = [];
    show("admin", calls);
    fireEvent.click(await screen.findByRole("button", { name: "Remove Lamp" }));
    await screen.findByText(/Automations that use it will show an error/);
    expect(calls).not.toContain("remove");
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(calls).toContain("remove"));
  });

  it("lets a viewer look, without scanning or changing anything", async () => {
    const calls: string[] = [];
    show("viewer", calls);
    await screen.findByText("Lamp");
    expect(screen.queryByRole("button", { name: "Remove Lamp" })).toBeNull();
    expect(calls).toEqual([]);
  });
});
