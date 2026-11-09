import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExtensionStatus, Integration } from "@/api";
import { mockService, WithRole, type Routes } from "@/test/helpers";
import { IntegrationsPage } from "./IntegrationsPage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const hue: Integration = {
  type: "hue", name: "Hue bridge", description: "Lights in the hall", devices: [
    { id: "a", name: "Lamp", kind: "light", online: true },
    { id: "b", name: "Hall lamp", kind: "light", online: false },
    { id: "c", name: "Spot", kind: "light", online: true },
  ],
};
const empty: Integration = { type: "smartlife", name: "Smart Life", devices: [] };
const addedExtension: ExtensionStatus = {
  source: "github:a/hue@v1", id: "hue", name: "Hue bridge", access: [], provides: ["devices"], running: true, removable: false, settings: [],
};

function serve(list: Integration[], extra: Routes = {}) {
  return mockService({ "GET /api/integrations": () => ({ body: list }), "GET /api/extensions/removed": () => ({ body: [] }), ...extra });
}
const show = (role: "viewer" | "admin" = "admin", go = vi.fn()) => render(<WithRole role={role}><IntegrationsPage go={go} /></WithRole>);

afterEach(cleanup);
beforeEach(() => vi.unstubAllGlobals());

describe("IntegrationsPage list", () => {
  it("shows each integration with how many of its devices are online", async () => {
    serve([hue, empty]);
    show();
    expect(await screen.findByText("Hue bridge")).toBeTruthy();
    expect(screen.getByText("Lights in the hall")).toBeTruthy();
    expect(screen.getByText((_, el) => el?.tagName === "P" && el.textContent === "2 of 3 online")).toBeTruthy();
    expect(screen.getByText("No devices from this yet")).toBeTruthy();
  });

  it("says there are no integrations yet, and where to add one", async () => {
    serve([]);
    show();
    expect(await screen.findByText("No integrations yet")).toBeTruthy();
    expect(screen.getByText(/list one under extensions in your config.json/)).toBeTruthy();
  });

  it("lets an administrator add one, and tells a viewer only an administrator can", async () => {
    serve([]);
    const { unmount } = show("admin");
    expect(await screen.findByRole("button", { name: "Add integration" })).toBeTruthy();
    expect(screen.queryByText("Adding or removing integrations needs an administrator.")).toBeNull();
    unmount();
    show("viewer");
    expect(await screen.findByText("Adding or removing integrations needs an administrator.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Add integration" })).toBeNull();
  });
});

describe("IntegrationsPage open", () => {
  it("opens an added integration by its type, and not one that was built in", async () => {
    const go = vi.fn();
    serve([{ ...hue, extension: addedExtension }, { ...empty }]);
    show("admin", go);
    await screen.findByText("Hue bridge");
    const openButtons = screen.getAllByRole("button", { name: "Open" }) as HTMLButtonElement[];
    expect(openButtons).toHaveLength(2);
    expect(openButtons[0].disabled).toBe(false);
    expect(openButtons[1].disabled).toBe(true);
    fireEvent.click(openButtons[0]);
    expect(go).toHaveBeenCalledWith("integrations", "hue");
  });

  it("warns on an integration that needs approval and names its state", async () => {
    serve([{ ...hue, extension: { ...addedExtension, running: false, needsApproval: true } }]);
    show();
    expect(await screen.findByText("Needs your OK")).toBeTruthy();
  });

  it("labels a built-in integration as built in, and doesn't let it be opened", async () => {
    serve([empty]);
    show();
    expect(await screen.findByText("Built in")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Open" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("IntegrationsPage removed add-ons", () => {
  it("offers to add back a removed integration", async () => {
    serve([], {
      "GET /api/extensions/removed": () => ({ body: [{ source: "github:a/tuya@v1", name: "Tuya", kind: "integration" }] }),
    });
    show();
    expect(await screen.findByText("Tuya")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Add back" })).toBeTruthy();
  });
});
