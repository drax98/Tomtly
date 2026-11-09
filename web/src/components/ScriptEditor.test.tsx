import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/api";
import { WithRole } from "@/test/helpers";
import { ScriptEditor } from "./ScriptEditor";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
// Monaco can't run here. Like the real one, this owns its text after mounting and saves on Ctrl+S.
vi.mock("./MonacoEditor", () => ({
  default: ({ value, onChange, onSave }: { value: string; onChange: (v: string) => void; onSave: () => void }) => (
    <textarea
      aria-label="Source of heating"
      defaultValue={value}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.ctrlKey && e.key === "s") onSave();
      }}
    />
  ),
}));

afterEach(cleanup);
beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "source").mockResolvedValue({ name: "heating", source: "old", version: "v1" });
});

const editBox = () => screen.findByRole("textbox", { name: "Source of heating" });

async function open(live = false) {
  render(<WithRole><ScriptEditor name="heating" live={live} onClose={vi.fn()} /></WithRole>);
  const box = await editBox();
  fireEvent.change(box, { target: { value: "new" } });
  return box;
}

describe("ScriptEditor", () => {
  it("saves with the version it opened, so a newer change can't be overwritten", async () => {
    const save = vi.spyOn(api, "saveSource").mockResolvedValue({ name: "heating", source: "new", version: "v2" });
    await open();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith("heating", "new", "v1", false));
  });

  it("says what a saved script still needs approved", async () => {
    vi.spyOn(api, "saveSource").mockResolvedValue({ name: "heating", source: "new", version: "v2", access: [
      { key: "device.write:a", label: "Change Boiler (a)", approved: false },
      { key: "device.read:b", label: "Read Sensor (b)", approved: true },
    ] });
    await open();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Change Boiler (a)")).toBeTruthy();
    expect(screen.queryByText("Read Sensor (b)")).toBeNull();
  });

  it("has nothing to save until something changes", async () => {
    render(<WithRole><ScriptEditor name="heating" live={false} onClose={vi.fn()} /></WithRole>);
    await editBox();
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("returns a live script to shadow by default, and keeps it live only when asked", async () => {
    const save = vi.spyOn(api, "saveSource").mockResolvedValue({ name: "heating", source: "new", version: "v2" });
    await open(true);
    fireEvent.click(screen.getByRole("button", { name: "Save and keep live" }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith("heating", "new", "v1", true));

    cleanup();
    save.mockClear();
    await open(true);
    fireEvent.click(screen.getByRole("button", { name: "Save to shadow" }));
    await waitFor(() => expect(save).toHaveBeenLastCalledWith("heating", "new", "v1", false));
    await screen.findByText("Shadow");
  });

  it("Ctrl+S never keeps a live script live", async () => {
    const save = vi.spyOn(api, "saveSource").mockResolvedValue({ name: "heating", source: "new", version: "v2" });
    const box = await open(true);
    fireEvent.keyDown(box, { key: "s", ctrlKey: true });
    await waitFor(() => expect(save).toHaveBeenCalledWith("heating", "new", "v1", false));
  });

  it("shows why a save failed and keeps the text", async () => {
    vi.spyOn(api, "saveSource").mockRejectedValue(new Error("heating.ts:3: Unexpected ;"));
    const box = await open();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("heating.ts:3: Unexpected ;");
    expect(screen.getByText("Nothing was saved")).toBeTruthy();
    expect((box as HTMLTextAreaElement).value).toBe("new");
  });

  it("offers the latest version after a conflict, and only replaces the text if you agree", async () => {
    const conflict = Object.assign(new Error("changed"), { status: 409 });
    vi.spyOn(api, "saveSource").mockRejectedValue(conflict);
    const box = await open();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const latest = await screen.findByRole("button", { name: "Load the latest version" });

    fireEvent.click(latest);
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect((box as HTMLTextAreaElement).value).toBe("new");

    fireEvent.click(latest);
    fireEvent.click(await screen.findByRole("button", { name: "Load it" }));
    await waitFor(async () => expect(((await editBox()) as HTMLTextAreaElement).value).toBe("old"));
  });

  it("asks before discarding unsaved changes", async () => {
    const onClose = vi.fn();
    render(<WithRole><ScriptEditor name="heating" live={false} onClose={onClose} /></WithRole>);
    fireEvent.change(await editBox(), { target: { value: "new" } });
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    await screen.findByText("Leave without saving?");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    fireEvent.click(await screen.findByRole("button", { name: "Discard changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("won't offer to keep a script live without the live permission", async () => {
    render(<WithRole role="operator"><ScriptEditor name="heating" live onClose={vi.fn()} canLive={false} /></WithRole>);
    await editBox();
    expect(screen.queryByRole("button", { name: "Save and keep live" })).toBeNull();
  });

  it("creates a new script with no version", async () => {
    const save = vi.spyOn(api, "saveSource").mockResolvedValue({ name: "fresh", source: "x", version: "v1" });
    render(<WithRole><ScriptEditor name="fresh" isNew live={false} onClose={vi.fn()} /></WithRole>);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(save).toHaveBeenCalledWith("fresh", expect.any(String), "", false));
    expect(api.source).not.toHaveBeenCalled();
  });
});
