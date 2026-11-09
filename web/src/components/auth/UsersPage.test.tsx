import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import type { ManagedUser, Role } from "@/authApi";
import { mockService, WithRole, type Routes } from "@/test/helpers";
import { UsersPage } from "./UsersPage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const person = (over: Partial<ManagedUser> = {}): ManagedUser => ({
  id: "u2", username: "bo", role: "operator", disabled: false, created: "2026-10-01T09:00:00Z", ...over,
});
const me = person({ id: "u1", username: "ann", role: "admin" });
const PASSWORD = "correct horse 1";

function serve(users: ManagedUser[], extra: Routes = {}) {
  return mockService({ "GET /api/users": () => ({ body: users }), ...extra });
}
const show = (role: Role = "admin") => render(<WithRole role={role}><UsersPage /></WithRole>);
const waitForRow = (name: string) => screen.findByRole("row", { name: new RegExp(`^${name}\\b`) });
const pick = async (trigger: string, option: RegExp) => {
  fireEvent.keyDown(screen.getByLabelText(trigger), { key: "Enter" });
  fireEvent.click(await screen.findByRole("option", { name: option }));
};

afterEach(cleanup);
beforeEach(() => vi.clearAllMocks());

describe("UsersPage list", () => {
  it("lists each person with their role, badges for you and for paused accounts, and when they were added", async () => {
    serve([me, person({ disabled: true, created: new Date(Date.now() - 2 * 86400_000).toISOString() })]);
    show();
    expect(await screen.findByText("bo")).toBeTruthy();
    expect(screen.getByText("You")).toBeTruthy();
    expect(screen.getByText("Paused")).toBeTruthy();
    expect(screen.getByLabelText("Role of bo")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Allow sign-in" })).toBeTruthy();
    expect(screen.getByLabelText("Role of ann").textContent).toContain("Administrator");
  });

  it("says there is no one yet, and offers to add the first person", async () => {
    serve([]);
    show();
    expect(await screen.findByText("No one yet")).toBeTruthy();
    // The header and the empty state both offer it.
    fireEvent.click(screen.getAllByRole("button", { name: "Add a person" }).at(-1) as HTMLElement);
    expect(await screen.findByRole("dialog")).toBeTruthy();
  });

  it("explains a failed load, and tries again", async () => {
    let up = false;
    serve([], { "GET /api/users": () => (up ? { body: [person()] } : { status: 500, body: { error: "db down" } }) });
    show();
    expect(await screen.findByText("Couldn't load the list")).toBeTruthy();
    expect(screen.getByText("db down")).toBeTruthy();
    up = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("bo")).toBeTruthy();
  });
});

describe("UsersPage add a person", () => {
  it("keeps Add off until there is a name and a password of at least twelve characters", async () => {
    serve([me]);
    show();
    await screen.findByText("ann");
    fireEvent.click(screen.getByRole("button", { name: "Add a person" }));
    const dialog = await screen.findByRole("dialog");
    const add = within(dialog).getByRole("button", { name: "Add person" }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: " cy " } });
    fireEvent.change(within(dialog).getByLabelText("Temporary password"), { target: { value: "elevenchars" } });
    expect(add.disabled).toBe(true);
    fireEvent.change(within(dialog).getByLabelText("Temporary password"), { target: { value: "twelvechars!" } });
    expect(add.disabled).toBe(false);
  });

  it("adds the person with the chosen role, then lists them and says they can sign in", async () => {
    const adding = vi.fn<(...args: unknown[]) => void>();
    let users = [me];
    const calls = serve([], {
      "GET /api/users": () => ({ body: users }),
      "POST /api/users": (body) => {
        adding(body);
        users = [me, person({ id: "u3", username: "cy", role: "editor" })];
        return { body: users[1] };
      },
    });
    show();
    await screen.findByText("ann");
    fireEvent.click(screen.getByRole("button", { name: "Add a person" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: " cy " } });
    fireEvent.change(within(dialog).getByLabelText("Temporary password"), { target: { value: PASSWORD } });
    await pick("Role", /^Editor/);
    fireEvent.click(within(dialog).getByRole("button", { name: "Add person" }));
    await waitFor(() => expect(screen.getByText("cy")).toBeTruthy());
    expect(adding).toHaveBeenCalledWith({ username: "cy", password: PASSWORD, role: "editor" });
    expect(toast.success).toHaveBeenCalledWith("cy can now sign in");
    expect(calls.filter((c) => c.key === "GET /api/users").length).toBeGreaterThan(1);
  });

  it("starts with Family as the role and shows why a person couldn't be added", async () => {
    const posted = serve([me], { "POST /api/users": () => ({ status: 400, body: { error: "that name is taken" } }) });
    show();
    await screen.findByText("ann");
    fireEvent.click(screen.getByRole("button", { name: "Add a person" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Family")).toBeTruthy();
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "bo" } });
    fireEvent.change(within(dialog).getByLabelText("Temporary password"), { target: { value: PASSWORD } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add person" }));
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(screen.getByText("that name is taken")).toBeTruthy();
    expect(posted.find((c) => c.key === "POST /api/users")?.body).toEqual({ username: "bo", password: PASSWORD, role: "operator" });
  });
});

describe("UsersPage changes", () => {
  it("changes another person's role straight away", async () => {
    const calls = serve([me, person()], { "PATCH /api/users/u2": () => ({ body: person({ role: "viewer" }) }) });
    show();
    await screen.findByText("bo");
    await pick("Role of bo", /^Viewer/);
    await waitFor(() => expect(calls.find((c) => c.key === "PATCH /api/users/u2")?.body).toEqual({ role: "viewer" }));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("asks before changing your own role, and changes nothing if you back out", async () => {
    const calls = serve([me], { "PATCH /api/users/u1": () => ({ body: me }) });
    show();
    await screen.findByText("ann");
    await pick("Role of ann", /^Editor/);
    expect(await screen.findByText("Change the role on your own account?")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByText("Change the role on your own account?")).toBeNull());
    expect(calls.some((c) => c.key === "PATCH /api/users/u1")).toBe(false);
  });

  it("pauses a person's sign-in, and allows it again", async () => {
    const calls = serve([me, person()], {
      "PATCH /api/users/u2": (body) => ({ body: person({ disabled: (body as { disabled?: boolean }).disabled ?? false }) }),
    });
    show();
    fireEvent.click(within(await waitForRow("bo")).getByRole("button", { name: "Pause" }));
    await waitFor(() => expect(calls.find((c) => c.key === "PATCH /api/users/u2")?.body).toEqual({ disabled: true }));
  });

  it("removes a person after they confirm, and tells them their tokens stop too", async () => {
    const calls = serve([me, person()], { "DELETE /api/users/u2": () => ({ body: {} }) });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Remove bo" }));
    expect(await screen.findByText("Remove bo?")).toBeTruthy();
    expect(screen.getByText(/Their API tokens stop working too/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(calls.some((c) => c.key === "DELETE /api/users/u2")).toBe(true));
  });

  it("shows the server's reason when it refuses a change, such as removing the last administrator", async () => {
    serve([me, person({ id: "u9", username: "root", role: "admin" })], {
      "DELETE /api/users/u9": () => ({ status: 409, body: { error: "can't remove the last administrator" } }),
    });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Remove root" }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    expect(await screen.findByText("can't remove the last administrator")).toBeTruthy();
  });
});

describe("UsersPage password", () => {
  it("keeps Set password off until the new one is twelve characters, then sets it", async () => {
    const calls = serve([me, person()], { "PATCH /api/users/u2": () => ({ body: person() }) });
    show();
    fireEvent.click(within(await waitForRow("bo")).getByRole("button", { name: "Set password" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("New password for bo")).toBeTruthy();
    expect(within(dialog).getByText("Tell them the new password in person. They can change it after signing in.")).toBeTruthy();
    const set = within(dialog).getByRole("button", { name: "Set password" }) as HTMLButtonElement;
    expect(set.disabled).toBe(true);
    fireEvent.change(within(dialog).getByLabelText("New password"), { target: { value: PASSWORD } });
    expect(set.disabled).toBe(false);
    fireEvent.click(set);
    await waitFor(() => expect(calls.find((c) => c.key === "PATCH /api/users/u2")?.body).toEqual({ password: PASSWORD }));
    expect(toast.success).toHaveBeenCalledWith("New password set for bo");
  });

  it("warns that setting your own password signs you out everywhere", async () => {
    serve([me]);
    show();
    fireEvent.click(within(await waitForRow("ann")).getByRole("button", { name: "Set password" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/signs you out everywhere/)).toBeTruthy();
  });

  it("shows why the password couldn't be set", async () => {
    serve([me, person()], { "PATCH /api/users/u2": () => ({ status: 400, body: { error: "too common" } }) });
    show();
    fireEvent.click(within(await waitForRow("bo")).getByRole("button", { name: "Set password" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("New password"), { target: { value: PASSWORD } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Set password" }));
    expect(await within(dialog).findByText("too common")).toBeTruthy();
  });
});
