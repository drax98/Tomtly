import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Welcome } from "./Welcome";

afterEach(cleanup);

describe("Welcome", () => {
  it("shows the order to go in, when open", () => {
    render(<Welcome open onClose={vi.fn()} go={vi.fn()} />);
    expect(screen.getByText("You're all set")).toBeTruthy();
    expect(screen.getByText(/Look at your devices/)).toBeTruthy();
    expect(screen.getByText(/Add the people who live here/)).toBeTruthy();
    expect(screen.getByText(/They start in practice mode/)).toBeTruthy();
  });

  it("stays shut when not open", () => {
    render(<Welcome open={false} onClose={vi.fn()} go={vi.fn()} />);
    expect(screen.queryByText("You're all set")).toBeNull();
  });

  it("closes, and goes to the people page to add them", () => {
    const onClose = vi.fn();
    const go = vi.fn();
    render(<Welcome open onClose={onClose} go={go} />);
    fireEvent.click(screen.getByRole("button", { name: "Add people" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(go).toHaveBeenCalledWith("users");
  });

  it("closes, and goes to the devices page", () => {
    const onClose = vi.fn();
    const go = vi.fn();
    render(<Welcome open onClose={onClose} go={go} />);
    fireEvent.click(screen.getByRole("button", { name: "See my devices" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(go).toHaveBeenCalledWith("devices");
  });

  it("closes without moving anywhere when dismissed", () => {
    const onClose = vi.fn();
    const go = vi.fn();
    render(<Welcome open onClose={onClose} go={go} />);
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
    expect(go).not.toHaveBeenCalled();
  });
});
