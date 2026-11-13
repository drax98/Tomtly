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
  source: "github:a/hue@v1", id: "hue", name: "Hue bridge", access: [], provides: ["devices"], running: true, settings: [],
};

function serve(list: Integration[], extra: Routes = {}) {
  return mockService({ "GET /api/integrations": () => ({ body: list }), ...extra });
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
    expect(screen.getByText(/Add one with the button at the top of this page/)).toBeTruthy();
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

  it("opens an added integration from anywhere on its card, once, and a built-in one not at all", async () => {
    const go = vi.fn();
    serve([{ ...hue, extension: addedExtension }, { ...empty }]);
    show("admin", go);
    fireEvent.click(await screen.findByText("Lights in the hall"));
    expect(go).toHaveBeenCalledTimes(1);
    expect(go).toHaveBeenCalledWith("integrations", "hue");
    fireEvent.click(screen.getByText("Smart Life"));
    expect(go).toHaveBeenCalledTimes(1);
  });

  it("warns on an integration that needs approval and names its state", async () => {
    serve([{ ...hue, extension: { ...addedExtension, running: false, needsApproval: true } }]);
    show();
    expect(await screen.findByText("Needs your OK")).toBeTruthy();
  });

  it("keeps the list to a summary: no settings or update buttons, and a badge when an update waits", async () => {
    serve([{ ...hue, extension: { ...addedExtension, version: "1.0.0", marketUpdate: { version: "1.1.0" } } }]);
    show();
    expect(await screen.findByText("Update 1.1.0")).toBeTruthy();
    for (const name of ["Edit settings", "Upload new version", "Update from GitHub", "Remove", "Update to 1.1.0"]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
    expect(screen.getByRole("button", { name: "Open" })).toBeTruthy();
    expect(screen.getByText("Version 1.0.0")).toBeTruthy();
    expect(screen.queryByText("What it can reach")).toBeNull();      // access, risks and settings are on its own page
    expect(screen.queryByText(/^Configured:/)).toBeNull();
  });

  it("shows no update badge when there's nothing newer", async () => {
    serve([{ ...hue, extension: addedExtension }]);
    show();
    await screen.findByText("Hue bridge");
    expect(screen.queryByText(/^Update /)).toBeNull();
  });

  it("labels a built-in integration as built in, and doesn't let it be opened", async () => {
    serve([empty]);
    show();
    expect(await screen.findByText("Built in")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Open" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
