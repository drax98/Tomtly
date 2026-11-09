import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "@/App";
import type { Permission, Role } from "@/authApi";
import { mockService, PERMISSIONS, type Routes } from "@/test/helpers";

vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }), Toaster: () => null }));
vi.mock("@/components/MonacoEditor", () => ({ default: () => <div /> }));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
beforeEach(() => {
  window.history.replaceState(null, "", "/devices"); // most of these are about devices; Home has its own tests
  localStorage.clear();
});

const signedIn = (role: Role) => ({ setupRequired: false, user: { id: "u1", username: "ann", role }, permissions: PERMISSIONS[role] as Permission[] });
const anonymous = { setupRequired: false, user: null, permissions: [] };

const light = { id: "d1", adapter: "t", name: "Lamp", kind: "light", category: "", product: "", online: true, dps: [{ dp: "1", code: "switch_led", type: "Boolean", values: {}, scale: 0, writable: true, value: false }] };
const base = (role: Role): Routes => ({
  "GET /api/auth/status": () => ({ body: signedIn(role) }),
  "GET /api/devices": () => ({ body: [light] }),
  "GET /api/rooms": () => ({ body: { Hall: ["d1"] } }),
  "GET /api/automations": () => ({ body: { scripts: [{ name: "heating", live: false }], events: [] } }),
  "GET /api/extensions": () => ({ body: [] }),
  "GET /api/integrations": () => ({ body: [] }),
});

describe("first run and sign-in", () => {
  it("creates the first account, explains there is no recovery, and welcomes you", async () => {
    let done = false;
    const calls = mockService({
      ...base("admin"),
      "GET /api/auth/status": () => ({ body: done ? signedIn("admin") : { setupRequired: true, user: null, permissions: [] } }),
      "POST /api/auth/setup": () => {
        done = true;
        return { body: signedIn("admin") };
      },
    });
    render(<App />);
    await screen.findByText(/no email recovery/);
    const create = screen.getByRole("button", { name: "Create account and start" }) as HTMLButtonElement;
    fireEvent.change(screen.getByLabelText("Your name for signing in"), { target: { value: "ann" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "short" } });
    fireEvent.change(screen.getByLabelText("Type the password again"), { target: { value: "short" } });
    expect(create.disabled).toBe(true);
    expect(screen.getByText(/at least 12/i)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "a long enough password" } });
    fireEvent.change(screen.getByLabelText("Type the password again"), { target: { value: "different one here!" } });
    expect(create.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Type the password again"), { target: { value: "a long enough password" } });
    fireEvent.click(create);
    await screen.findByText("You're all set");
    expect(calls.find((c) => c.key === "POST /api/auth/setup")?.body).toEqual({ username: "ann", password: "a long enough password" });
  });

  it("asks for the setup code only when the service does, and says where it is", async () => {
    let done = false;
    const calls = mockService({
      ...base("admin"),
      "GET /api/auth/status": () => ({ body: done ? signedIn("admin") : { setupRequired: true, setupTokenRequired: true, user: null, permissions: [] } }),
      "POST /api/auth/setup": () => {
        done = true;
        return { body: signedIn("admin") };
      },
    });
    render(<App />);
    const code = await screen.findByLabelText("Setup code");
    expect(screen.getByText(/in the service's logs/)).toBeTruthy();
    const create = screen.getByRole("button", { name: "Create account and start" }) as HTMLButtonElement;
    fireEvent.change(screen.getByLabelText("Your name for signing in"), { target: { value: "ann" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "a long enough password" } });
    fireEvent.change(screen.getByLabelText("Type the password again"), { target: { value: "a long enough password" } });
    expect(create.disabled).toBe(true);
    fireEvent.change(code, { target: { value: "from-the-logs" } });
    fireEvent.click(create);
    await screen.findByText("You're all set");
    expect(calls.find((c) => c.key === "POST /api/auth/setup")?.body).toEqual({ username: "ann", password: "a long enough password", setupToken: "from-the-logs" });
  });

  it("deletes API tokens with a password change unless told not to", async () => {
    const calls = mockService({ ...base("admin"), "POST /api/auth/password": () => ({ body: { changed: true } }) });
    render(<App />);
    await screen.findByRole("switch", { name: /Lamp/ });
    const change = async (keep: boolean) => {
      fireEvent.pointerDown(screen.getByRole("button", { name: /Account menu/ }), { button: 0, ctrlKey: false });
      fireEvent.click(await screen.findByRole("menuitem", { name: /Change password/ }));
      fireEvent.change(await screen.findByLabelText("Current password"), { target: { value: "the old password" } });
      fireEvent.change(screen.getByLabelText("New password"), { target: { value: "a brand new password" } });
      fireEvent.change(screen.getByLabelText("New password again"), { target: { value: "a brand new password" } });
      const box = screen.getByRole("checkbox", { name: /Also delete my API tokens/ }) as HTMLInputElement;
      expect(box.checked).toBe(true);
      if (keep) fireEvent.click(box);
      fireEvent.click(screen.getByRole("button", { name: "Change password" }));
      await waitFor(() => expect(screen.queryByLabelText("Current password")).toBeNull());
    };
    await change(false);
    await change(true);
    const sent = calls.filter((c) => c.key === "POST /api/auth/password").map((c) => (c.body as { keepTokens: boolean }).keepTokens);
    expect(sent).toEqual([false, true]);
  });

  it("shows a wrong password on the form instead of leaving it, and a lock-out message on 429", async () => {
    let status = 401;
    mockService({ ...base("admin"), "GET /api/auth/status": () => ({ body: anonymous }), "POST /api/auth/login": () => ({ status, body: { error: "bad" } }) });
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "ann" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "wrong" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByText(/name or password isn't right/);
    expect(screen.getByRole("button", { name: "Sign in" })).toBeTruthy(); // still the login screen, no loop
    status = 429;
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByText("Too many attempts, try again shortly");
  });
});

describe("a lost session", () => {
  it("goes back to sign-in on a 401 and keeps the page you were on", async () => {
    let expired = false;
    mockService({
      ...base("admin"),
      "GET /api/automations": () => (expired ? { status: 401, body: { error: "sign in" } } : { body: { scripts: [{ name: "heating", live: false }], events: [] } }),
      "POST /api/automations/reload": () => ({ status: 401, body: { error: "sign in" } }),
      "POST /api/auth/login": () => ({ body: signedIn("admin") }),
    });
    window.history.replaceState(null, "", "/automations");
    render(<App />);
    await screen.findByText("Heating");
    expired = true;
    fireEvent.click(screen.getByRole("button", { name: "Reload from disk" }));
    await screen.findByText("Sign in again to continue");
    expect(window.location.pathname).toBe("/automations");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ann" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "pw" } });
    expired = false;
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(screen.queryByText("Sign in again to continue")).toBeNull());
    expect(screen.getByRole("heading", { name: "Automations" })).toBeTruthy();
  });
});

describe("what each role sees", () => {
  it("lets a viewer look at devices but not change them, and says why", async () => {
    mockService(base("viewer"));
    render(<App />);
    const lamp = await screen.findByRole("switch", { name: /Lamp/ });
    expect((lamp as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/only change things with the Family role/)).toBeTruthy();
    expect(screen.queryByRole("link", { name: /People/ })).toBeNull();
  });

  it("lets a family member switch things on", async () => {
    mockService(base("operator"));
    render(<App />);
    const lamp = await screen.findByRole("switch", { name: /Lamp/ });
    expect((lamp as HTMLButtonElement).disabled).toBe(false);
  });

  it.each([
    ["operator", false, false],
    ["editor", true, true],
  ] as const)("%s: editing %s, going live %s on automations", async (role, edit, live) => {
    mockService(base(role));
    window.history.replaceState(null, "", "/automations");
    render(<App />);
    await screen.findByText("Heating");
    expect(!!screen.queryByRole("button", { name: "Edit heating" })).toBe(edit);
    expect(!!screen.queryByLabelText("New script name")).toBe(edit);
    expect((screen.getByRole("switch", { name: "Run heating live" }) as HTMLButtonElement).disabled).toBe(!live);
  });

  it("shows People and the activity log only to administrators", async () => {
    mockService(base("admin"));
    render(<App />);
    await screen.findByRole("switch", { name: /Lamp/ });
    expect(screen.getByRole("link", { name: /People/ })).toBeTruthy();
    expect(screen.getByRole("link", { name: /Activity log/ })).toBeTruthy();
  });

  it("keeps Add, Approve and Remove for people who manage extensions", async () => {
    mockService(base("editor"));
    window.history.replaceState(null, "", "/plugins");
    render(<App />);
    await screen.findByText(/needs an administrator/);
    expect(screen.queryByRole("button", { name: "Add plugin" })).toBeNull();
  });
});

describe("people and tokens", () => {
  it("shows the server's last-administrator refusal", async () => {
    const admin = { id: "u1", username: "ann", role: "admin", disabled: false, created: "2026-10-01T10:00:00Z" };
    mockService({
      ...base("admin"),
      "GET /api/users": () => ({ body: [admin] }),
      "PATCH /api/users/u1": () => ({ status: 409, body: { error: "there must stay at least one enabled admin" } }),
    });
    window.history.replaceState(null, "", "/users");
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Pause" })); // the name alone also shows in the sidebar, before the list loads
    // It's the signed-in account, so the dialog warns about being signed out first.
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await screen.findByText("there must stay at least one enabled admin");
  });

  it("shows a new token's secret once, and never again after it is dismissed or reloaded", async () => {
    const tokens: unknown[] = [];
    mockService({
      ...base("admin"),
      "GET /api/tokens": () => ({ body: tokens }),
      "POST /api/tokens": () => {
        tokens.push({ id: "t1", name: "Kitchen", role: "viewer", owner: "ann", created: "2026-11-01T10:00:00Z", lastUsed: null });
        return { status: 201, body: { ...(tokens[0] as object), token: "hc_secret_value" } };
      },
    });
    window.history.replaceState(null, "", "/tokens");
    render(<App />);
    fireEvent.change(await screen.findByLabelText("What is it for?"), { target: { value: "Kitchen" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    const secret = await screen.findByTestId("token-secret");
    expect(secret.textContent).toBe("hc_secret_value");
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
    expect(screen.getByText(/only time it is shown/)).toBeTruthy();
    expect(within(await screen.findByRole("table")).queryByText("hc_secret_value")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "I've saved it" }));
    expect(screen.queryByText("hc_secret_value")).toBeNull();
    expect(JSON.stringify({ ...localStorage })).not.toContain("hc_secret");
    expect(window.location.href).not.toContain("hc_secret");
  });

  it("moves between pages by path, and tidies an old link with a hash", async () => {
    mockService({ ...base("admin"), "GET /api/plugins": () => ({ body: [] }) });
    window.history.replaceState(null, "", "/#plugins");
    render(<App />);
    await waitFor(() => expect(window.location.pathname + window.location.hash).toBe("/plugins"));
    fireEvent.click(await screen.findByRole("link", { name: "Devices" }));
    expect(window.location.pathname).toBe("/devices");
    expect(screen.getByRole("link", { name: "Devices" }).getAttribute("aria-current")).toBe("page");
    window.history.back();
    await waitFor(() => expect(window.location.pathname).toBe("/plugins"));
    await waitFor(() => expect(screen.getByRole("link", { name: "Plugins" }).getAttribute("aria-current")).toBe("page"));
  });

  it.each(["/integrations/xiaomi", "/automations/heating"])("signs in and lands on %s, the page asked for", async (path) => {
    let signed = false;
    mockService({
      ...base("admin"),
      "GET /api/auth/status": () => ({ body: signed ? signedIn("admin") : anonymous }),
      "POST /api/auth/login": () => {
        signed = true;
        return { body: signedIn("admin") };
      },
    });
    localStorage.setItem("lastPage", "devices");
    window.history.replaceState(null, "", path);
    render(<App />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "ann" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "pw" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    const page = path.split("/")[1];
    await waitFor(() => expect(screen.getByRole("link", { name: page === "automations" ? "Automations" : "Integrations" }).getAttribute("aria-current")).toBe("page"));
    expect(window.location.pathname).toBe(path);
  });

  it("calls a wrong current password what it is, not a permissions problem", async () => {
    mockService({ ...base("admin"), "POST /api/auth/password": () => ({ status: 403, body: { error: "the current password is wrong" } }) });
    render(<App />);
    await screen.findByRole("switch", { name: /Lamp/ });
    fireEvent.pointerDown(screen.getByRole("button", { name: /Account menu/ }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: /Change password/ }));
    fireEvent.change(await screen.findByLabelText("Current password"), { target: { value: "nope" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "a brand new password" } });
    fireEvent.change(screen.getByLabelText("New password again"), { target: { value: "a brand new password" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    await screen.findByText("Current password is wrong");
  });
});
