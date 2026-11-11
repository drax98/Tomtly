import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuditEntry } from "@/authApi";
import { mockService } from "@/test/helpers";
import { AuditPage } from "./AuditPage";

const entry = (over: Partial<AuditEntry>): AuditEntry => ({ time: "2026-11-01T12:00:00Z", user: "ann", action: "sign-in", detail: "", ip: "10.0.0.2", ...over });

afterEach(cleanup);
beforeEach(() => vi.unstubAllGlobals());

describe("AuditPage", () => {
  it("lists each change with who made it, what it was and where it came from, in the order given", async () => {
    mockService({
      "GET /api/audit": () => ({
        body: [
          entry({ user: "ann", action: "role changed", detail: "bo: viewer to editor", ip: "10.0.0.5" }),
          entry({ user: "", action: "sign-in failed", detail: "", ip: "10.0.0.9" }),
        ],
      }),
    });
    render(<AuditPage />);
    expect(await screen.findByText("role changed")).toBeTruthy();
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    const first = within(rows[0]).getAllByRole("cell").map((c) => c.textContent);
    expect(first.slice(1)).toEqual(["ann", "role changed", "bo: viewer to editor", "10.0.0.5"]);
    const second = within(rows[1]).getAllByRole("cell").map((c) => c.textContent);
    expect(second.slice(1)).toEqual(["–", "sign-in failed", "", "10.0.0.9"]);
  });

  it("says when nothing has been recorded", async () => {
    mockService({ "GET /api/audit": () => ({ body: [] }) });
    render(<AuditPage />);
    expect(await screen.findByText("Nothing recorded yet")).toBeTruthy();
  });

  it("explains a failed load, and tries again on request", async () => {
    let up = false;
    mockService({
      "GET /api/audit": () => (up ? { body: [entry({ action: "back online" })] } : { status: 500, body: { error: "database locked" } }),
    });
    render(<AuditPage />);
    expect(await screen.findByText("Couldn't load the log")).toBeTruthy();
    expect(screen.getByText("database locked")).toBeTruthy();
    up = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("back online")).toBeTruthy();
  });

  it("reloads from the Refresh button, keeping the list it has when a reload fails", async () => {
    let fail = false;
    let rows = [entry({ action: "first" })];
    mockService({
      "GET /api/audit": () => (fail ? { status: 500, body: { error: "gone" } } : { body: rows }),
    });
    render(<AuditPage />);
    expect(await screen.findByText("first")).toBeTruthy();
    rows = [entry({ action: "second" })];
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByText("second")).toBeTruthy();
    fail = true;
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByText("second")).toBeTruthy();
    expect(screen.queryByText("Couldn't load the log")).toBeNull();
  });
});
