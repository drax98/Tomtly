import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AutomationStatus, ExtensionStatus, PluginStatus } from "@/api";
import { mockService, WithRole, type Routes } from "@/test/helpers";
import { PluginsPage } from "./PluginsPage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const extension = (over: Partial<ExtensionStatus>): ExtensionStatus => ({
  source: "github:a/weather@v1", id: "weather", name: "Weather", access: [], provides: ["data"], running: true, settings: [], ...over,
});
const plugin = (over: Partial<PluginStatus>): PluginStatus => ({ id: "weather", type: "plugin", ...over });

function serve(automations: Partial<AutomationStatus>, extensions: ExtensionStatus[], extra: Routes = {}) {
  return mockService({
    "GET /api/automations": () => ({ body: { scripts: [], events: [], plugins: [], ...automations } }),
    "GET /api/extensions": () => ({ body: extensions }),
    ...extra,
  });
}
const show = (role: "viewer" | "admin" = "admin") => render(<WithRole role={role}><PluginsPage /></WithRole>);

afterEach(cleanup);
beforeEach(() => vi.unstubAllGlobals());

describe("PluginsPage", () => {
  it("says there are no plugins yet, and offers to add one to an administrator", async () => {
    serve({}, []);
    show();
    expect(await screen.findByText("No plugins yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Add plugin" })).toBeTruthy();
  });

  it("tells a viewer that only an administrator can add or remove plugins", async () => {
    serve({}, []);
    show("viewer");
    expect(await screen.findByText("Adding or removing plugins needs an administrator.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Add plugin" })).toBeNull();
  });

  it("shows a built-in plugin by its id, before it has any data", async () => {
    serve({ plugins: [plugin({ id: "clock" })] }, []);
    show();
    expect(await screen.findByText("clock")).toBeTruthy();
    expect(screen.getByText("Built in")).toBeTruthy();
    expect(screen.getByText("No data yet")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Details for scripts" })).toBeNull();
    expect(screen.queryByText("No plugins yet")).toBeNull();
  });

  it("says when a built-in plugin last updated, and shows its data and actions for scripts", async () => {
    serve({ plugins: [plugin({ id: "clock", updatedAt: "2026-11-01T12:00:00Z", data: { now: 5 }, actions: ["ping", "reset"] })] }, []);
    show();
    expect(await screen.findByText(/^Updated \d/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Details for scripts" }));
    expect(await screen.findByText('home.plugin("clock")')).toBeTruthy();
    expect(screen.getByText("Actions: ping, reset")).toBeTruthy();
    expect(screen.getByText(/"now": 5/)).toBeTruthy();
  });

  it("shows the last failed fetch with its error", async () => {
    serve({ plugins: [plugin({ id: "clock", error: "timeout after 5s" })] }, []);
    show();
    expect(await screen.findByText("The last fetch failed")).toBeTruthy();
    expect(screen.getByText("timeout after 5s")).toBeTruthy();
  });

  it("lists an add-on by its name, with its state and what it's for, once", async () => {
    serve({ plugins: [plugin({ id: "weather", updatedAt: "2026-11-01T12:00:00Z" })] }, [extension({ description: "Forecast for the week" })]);
    show();
    expect(await screen.findByText("Forecast for the week")).toBeTruthy();
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getByText(/^Updated \d/)).toBeTruthy();
    expect(screen.getAllByText("Weather")).toHaveLength(1);
    expect(screen.queryByText("Built in")).toBeNull();
  });

  it("names an add-on that is waiting for approval", async () => {
    serve({}, [extension({ running: false, needsApproval: true })]);
    show();
    expect(await screen.findByText("Needs your OK")).toBeTruthy();
  });

  it("lists an add-on that failed before it said what it provides, with its problem", async () => {
    serve({}, [extension({ id: undefined, name: undefined, provides: [], error: "bad manifest" })]);
    show();
    expect(await screen.findByText("Problem")).toBeTruthy();
    expect(screen.getByText("bad manifest")).toBeTruthy();
  });

  it("leaves out an add-on that provides only devices, but keeps its plugin visible", async () => {
    serve({ plugins: [plugin({ id: "hue", updatedAt: "2026-11-01T12:00:00Z" })] }, [extension({ id: "hue", name: "Hue bridge", provides: ["devices"] })]);
    show();
    // Its plugin twin still shows, by id, as a built-in.
    expect(await screen.findByText("hue")).toBeTruthy();
    expect(screen.queryByText("Hue bridge")).toBeNull();
  });

  it("gives no update line for an add-on that doesn't provide data", async () => {
    serve({ plugins: [plugin({ id: "tides", updatedAt: "2026-11-01T12:00:00Z" })] }, [extension({ id: "tides", name: "Tides", provides: ["actions"] })]);
    show();
    expect(await screen.findByText("Tides")).toBeTruthy();
    expect(screen.queryByText(/^Updated/)).toBeNull();
  });
});
