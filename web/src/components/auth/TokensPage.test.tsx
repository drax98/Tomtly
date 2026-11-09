import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import type { ApiToken, Role } from "@/authApi";
import { mockService, WithRole, type Routes } from "@/test/helpers";
import { TokensPage } from "./TokensPage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const token = (over: Partial<ApiToken> = {}): ApiToken => ({
  id: "t1", name: "Kitchen dashboard", role: "viewer", owner: "ann", created: "2026-10-01T09:00:00Z", lastUsed: null, ...over,
});

function serve(tokens: ApiToken[], extra: Routes = {}) {
  return mockService({ "GET /api/tokens": () => ({ body: tokens }), ...extra });
}
const show = (role: Role = "admin") => render(<WithRole role={role}><TokensPage /></WithRole>);
const writeText = vi.fn(async () => {});

afterEach(cleanup);
beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
});

describe("TokensPage list", () => {
  it("lists each token with its role, when it was last used and who owns it", async () => {
    serve([token({ lastUsed: new Date(Date.now() - 3 * 3600_000).toISOString() })]);
    show("admin");
    expect(await screen.findByText("Kitchen dashboard")).toBeTruthy();
    const row = screen.getByRole("row", { name: /Kitchen dashboard/ });
    expect(within(row).getByText("Viewer")).toBeTruthy();
    expect(within(row).getByText("3 h ago")).toBeTruthy();
    expect(within(row).getByText("ann")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Owner" })).toBeTruthy();
  });

  it("hides the owner column from someone who can't manage users, and says a token was never used", async () => {
    serve([token()]);
    show("editor");
    expect(await screen.findByText("Kitchen dashboard")).toBeTruthy();
    expect(screen.queryByRole("columnheader", { name: "Owner" })).toBeNull();
    expect(screen.getByText("Never")).toBeTruthy();
  });

  it("says there are no tokens, and that most people don't need one", async () => {
    serve([]);
    show();
    expect(await screen.findByText("No tokens yet")).toBeTruthy();
  });

  it("explains a failed load, and tries again", async () => {
    let up = false;
    serve([token()], { "GET /api/tokens": () => (up ? { body: [token()] } : { status: 500, body: { error: "db down" } }) });
    show();
    expect(await screen.findByText("Couldn't load your tokens")).toBeTruthy();
    expect(screen.getByText("db down")).toBeTruthy();
    up = true;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Kitchen dashboard")).toBeTruthy();
  });
});

describe("TokensPage create", () => {
  it("keeps Create off until the token has a name", async () => {
    serve([]);
    show();
    await screen.findByText("No tokens yet");
    const create = screen.getByRole("button", { name: "Create token" });
    expect((create as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("What is it for?"), { target: { value: "   " } });
    expect((create as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("What is it for?"), { target: { value: "  Phone  " } });
    expect((create as HTMLButtonElement).disabled).toBe(false);
  });

  it("creates the token with its trimmed name and role, and shows the secret once", async () => {
    const calls = serve([], {
      "POST /api/tokens": () => ({ body: { ...token({ name: "Phone", role: "operator" }), token: "tok_secret_123" } }),
    });
    show();
    await screen.findByText("No tokens yet");
    fireEvent.change(screen.getByLabelText("What is it for?"), { target: { value: "  Phone  " } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    expect(await screen.findByText('Copy the token for "Phone" now')).toBeTruthy();
    expect(screen.getByTestId("token-secret").textContent).toBe("tok_secret_123");
    expect(calls.find((c) => c.key === "POST /api/tokens")?.body).toEqual({ name: "Phone", role: "viewer" });
    expect((screen.getByLabelText("What is it for?") as HTMLInputElement).value).toBe("");
  });

  it("offers only the roles at or below the signed-in person's own", async () => {
    serve([]);
    show("editor");
    await screen.findByText("No tokens yet");
    fireEvent.keyDown(screen.getByLabelText("Token role"), { key: "Enter" });
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => within(o).getByText(/^[A-Za-z]+$/).textContent)).toEqual(["Viewer", "Family", "Editor"]);
  });

  it("shows why a token couldn't be created", async () => {
    serve([], { "POST /api/tokens": () => ({ status: 400, body: { error: "a token with that name already exists" } }) });
    show();
    await screen.findByText("No tokens yet");
    fireEvent.change(screen.getByLabelText("What is it for?"), { target: { value: "Phone" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    expect(await screen.findByText("a token with that name already exists")).toBeTruthy();
  });

  it("copies the secret, then hides it once it's saved", async () => {
    serve([], { "POST /api/tokens": () => ({ body: { ...token({ name: "Phone" }), token: "tok_abc" } }) });
    show();
    await screen.findByText("No tokens yet");
    fireEvent.change(screen.getByLabelText("What is it for?"), { target: { value: "Phone" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    await screen.findByTestId("token-secret");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Copied" })).toBeTruthy());
    expect(writeText).toHaveBeenCalledWith("tok_abc");
    fireEvent.click(screen.getByRole("button", { name: "I've saved it" }));
    expect(screen.queryByTestId("token-secret")).toBeNull();
  });

  it("says so when the clipboard isn't available", async () => {
    writeText.mockRejectedValueOnce(new Error("denied"));
    serve([], { "POST /api/tokens": () => ({ body: { ...token({ name: "Phone" }), token: "tok_abc" } }) });
    show();
    await screen.findByText("No tokens yet");
    fireEvent.change(screen.getByLabelText("What is it for?"), { target: { value: "Phone" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    await screen.findByTestId("token-secret");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't copy. Select the text and copy it yourself."));
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
  });
});

describe("TokensPage delete", () => {
  it("asks first, and deletes the token only when confirmed", async () => {
    const calls = serve([token()], { "DELETE /api/tokens/t1": () => ({ body: {} }) });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Delete Kitchen dashboard" }));
    expect(await screen.findByText('Delete "Kitchen dashboard"?')).toBeTruthy();
    expect(screen.getByText(/will stop working straight away/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByText('Delete "Kitchen dashboard"?')).toBeNull());
    expect(calls.some((c) => c.key.startsWith("DELETE"))).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "Delete Kitchen dashboard" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete token" }));
    await waitFor(() => expect(calls.some((c) => c.key === "DELETE /api/tokens/t1")).toBe(true));
  });

  it("shows why a token couldn't be deleted", async () => {
    serve([token()], { "DELETE /api/tokens/t1": () => ({ status: 500, body: { error: "disk full" } }) });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Delete Kitchen dashboard" }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete token" }));
    expect(await screen.findByText("disk full")).toBeTruthy();
  });
});
