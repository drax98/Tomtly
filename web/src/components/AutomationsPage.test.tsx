import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, type AutomationStatus } from "@/api";
import { WithRole } from "@/test/helpers";
import { AutomationsPage } from "./AutomationsPage";

vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }) }));
const editor = vi.hoisted(() => ({ canLive: undefined as boolean | undefined }));
vi.mock("./ScriptEditor", () => ({ ScriptEditor: ({ canLive }: { canLive?: boolean }) => { editor.canLive = canLive; return null; } }));

const status = (live: boolean): AutomationStatus => ({
  plugins: [],
  events: [{ time: "2026-11-01T12:00:00Z", script: "heating", kind: "refused", message: "Change Boiler (tuya:1): not approved, so it was refused" }],
  scripts: [{
    name: "heating",
    live,
    access: [
      { key: "device.write:tuya:1", label: "Change Boiler (tuya:1)", approved: false, refused: 3 },
      { key: "plugin.action:phone:notify", label: 'Run "notify" on the phone plugin', approved: false },
      { key: "device.read:tuya:2", label: "Read Sensor (tuya:2)", approved: true },
    ],
  }],
});

const show = (role: "viewer" | "editor" | "admin", live = false) => {
  vi.spyOn(api, "automations").mockResolvedValue(status(live));
  render(<WithRole role={role}><AutomationsPage routeName="" go={vi.fn()} /></WithRole>);
};

afterEach(cleanup);
beforeEach(() => vi.restoreAllMocks());

describe("AutomationsPage access", () => {
  it("offers keeping a script live only to someone who may approve what its new code uses", async () => {
    for (const [role, offered] of [["editor", false], ["admin", true]] as const) {
      vi.spyOn(api, "automations").mockResolvedValue(status(true));
      render(<WithRole role={role}><AutomationsPage routeName="heating" go={vi.fn()} /></WithRole>);
      await waitFor(() => expect(editor.canLive).toBe(offered));
      cleanup();
      editor.canLive = undefined;
    }
  });

  it("lists what a practising script needs, in words, with how often it was refused", async () => {
    show("admin");
    expect(await screen.findByText("Needs your approval before it goes live")).toBeTruthy();
    expect(screen.getByText(/refused 3 times/)).toBeTruthy();
    expect(screen.getByRole("button", { name: 'Approve: Run "notify" on the phone plugin' })).toBeTruthy();
    expect(screen.getByText("Refused")).toBeTruthy();
  });

  it("says plainly when a running script is being refused", async () => {
    show("admin", true);
    expect(await screen.findByText("Running, but these calls are refused")).toBeTruthy();
  });

  it("approves one thing by its key", async () => {
    const set = vi.spyOn(api, "setAccess").mockResolvedValue(status(false));
    show("admin");
    fireEvent.click(await screen.findByRole("button", { name: "Approve: Change Boiler (tuya:1)" }));
    await waitFor(() => expect(set).toHaveBeenCalledWith("heating", ["device.write:tuya:1"], true));
  });

  it("approves all that are waiting, and nothing already approved", async () => {
    const set = vi.spyOn(api, "setAccess").mockResolvedValue(status(false));
    show("admin");
    fireEvent.click(await screen.findByRole("button", { name: "Approve all 2" }));
    await waitFor(() => expect(set).toHaveBeenCalledWith("heating", ["device.write:tuya:1", "plugin.action:phone:notify"], true));
  });

  it("withdraws an approval", async () => {
    const set = vi.spyOn(api, "setAccess").mockResolvedValue(status(false));
    show("admin");
    fireEvent.click(await screen.findByRole("button", { name: "Withdraw: Read Sensor (tuya:2)", hidden: true }));
    await waitFor(() => expect(set).toHaveBeenCalledWith("heating", ["device.read:tuya:2"], false));
  });

  it("shows but doesn't offer approval to anyone else", async () => {
    show("editor");
    expect(await screen.findByText("An administrator has to approve these.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Approve/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Withdraw/, hidden: true })).toBeNull();
  });

  it("warns before going live with access still unapproved", async () => {
    show("admin");
    fireEvent.click(await screen.findByRole("switch", { name: "Run heating live" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/they will be refused/)).toBeTruthy();
    expect(within(dialog).getByText("Change Boiler (tuya:1)")).toBeTruthy();
  });
});
