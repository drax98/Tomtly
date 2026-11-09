import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, call, request, session } from "@/api";
import { authApi } from "@/authApi";

// Answers 200 to everything and records "METHOD path?query" with the parsed body, so each call can be checked.
function recordCalls() {
  const calls: { key: string; body: unknown }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ key: `${init?.method ?? "GET"} ${url.pathname}${url.search}`, body });
    return { ok: true, status: 200, json: async () => ({}) } as Response;
  }));
  return calls;
}

beforeEach(() => {
  session.expired = false;
  session.onExpired = () => {};
});
afterEach(() => vi.unstubAllGlobals());

describe("api endpoints", () => {
  it("sends each call to its path, method and body", async () => {
    const calls = recordCalls();
    const req = (key: string) => calls.find((c) => c.key === key);

    await api.devices();
    expect(req("GET /api/devices")).toBeTruthy();
    await api.rooms();
    await api.write("dev/1", "1", true);
    expect(calls.at(-1)).toEqual({ key: "POST /api/devices/dev%2F1/dps/1", body: { value: true } });
    await api.automations();
    await api.extensions();
    await api.integrations();
    await api.discoverDevices("hue");
    expect(calls.at(-1)?.key).toBe("POST /api/integrations/hue/discover");
    await api.removeDevice("a b");
    expect(calls.at(-1)?.key).toBe("DELETE /api/devices/a%20b");
    await api.restoreDevice("a");
    expect(calls.at(-1)?.key).toBe("POST /api/devices/a/restore");
    await api.marketplace();
    await api.inspectMarketEntry("tides");
    expect(calls.at(-1)).toEqual({ key: "POST /api/marketplace/inspect", body: { id: "tides" } });
    await api.marketReviews("tides", 20);
    expect(calls.at(-1)?.key).toBe("GET /api/marketplace/tides/reviews?offset=20");
    await api.postMarketReview("tides", { rating: 5, text: "ok", name: "Ann" });
    expect(calls.at(-1)).toEqual({ key: "POST /api/marketplace/tides/reviews", body: { rating: 5, text: "ok", name: "Ann" } });
    await api.inspectExtension("git:x");
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/inspect", body: { source: "git:x" } });
    await api.addExtension("s", { a: 1 }, ["x"]);
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions", body: { source: "s", settings: { a: 1 }, access: ["x"] } });
    await api.approveExtension("s", ["x"]);
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/approve", body: { source: "s", access: ["x"] } });
    await api.stopExtension("s");
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/stop", body: { source: "s" } });
    await api.updateExtensionSettings("s", { k: "v" });
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/settings", body: { source: "s", settings: { k: "v" } } });
    await api.replaceExtension("s", "n", undefined, ["x"]);
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/replace", body: { source: "s", newSource: "n", settings: undefined, access: ["x"] } });
    await api.newWebhookAddress("s");
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/webhook-token", body: { source: "s" } });
    await api.removedExtensions();
    expect(calls.at(-1)?.key).toBe("GET /api/extensions/removed");
    await api.restoreExtension("s");
    expect(calls.at(-1)).toEqual({ key: "POST /api/extensions/restore", body: { source: "s" } });
    await api.removeExtension("a&b");
    expect(calls.at(-1)?.key).toBe("DELETE /api/extensions?source=a%26b");
    await api.setLive("heat ing", true);
    expect(calls.at(-1)).toEqual({ key: "POST /api/automations/heat%20ing/live", body: { live: true } });
    await api.setAccess("heating", ["k"], false);
    expect(calls.at(-1)).toEqual({ key: "POST /api/automations/heating/access", body: { keys: ["k"], approved: false } });
    await api.reload();
    expect(calls.at(-1)).toEqual({ key: "POST /api/automations/reload", body: {} });
    await api.source("heating");
    expect(calls.at(-1)?.key).toBe("GET /api/automations/heating/source");
    await api.saveSource("heating", "code", "v2", true);
    expect(calls.at(-1)).toEqual({ key: "POST /api/automations/heating/source", body: { source: "code", version: "v2", keepLive: true } });
    await api.sdk();
    expect(calls.at(-1)?.key).toBe("GET /api/automations/sdk");
  });

  it("sends the auth calls with their methods and bodies", async () => {
    const calls = recordCalls();
    await authApi.status();
    await authApi.setup("ann", "pw", "tok");
    expect(calls.at(-1)).toEqual({ key: "POST /api/auth/setup", body: { username: "ann", password: "pw", setupToken: "tok" } });
    await authApi.login("ann", "pw");
    expect(calls.at(-1)).toEqual({ key: "POST /api/auth/login", body: { username: "ann", password: "pw" } });
    await authApi.logout();
    expect(calls.at(-1)).toEqual({ key: "POST /api/auth/logout", body: {} });
    await authApi.changePassword("old", "new", true);
    expect(calls.at(-1)).toEqual({ key: "POST /api/auth/password", body: { current: "old", new: "new", keepTokens: true } });
    await authApi.users();
    expect(calls.at(-1)?.key).toBe("GET /api/users");
    await authApi.createUser("bo", "pw", "viewer");
    expect(calls.at(-1)).toEqual({ key: "POST /api/users", body: { username: "bo", password: "pw", role: "viewer" } });
    await authApi.updateUser("u/1", { disabled: true });
    expect(calls.at(-1)).toEqual({ key: "PATCH /api/users/u%2F1", body: { disabled: true } });
    await authApi.deleteUser("u1");
    expect(calls.at(-1)?.key).toBe("DELETE /api/users/u1");
    await authApi.tokens();
    expect(calls.at(-1)?.key).toBe("GET /api/tokens");
    await authApi.createToken("phone", "editor");
    expect(calls.at(-1)).toEqual({ key: "POST /api/tokens", body: { name: "phone", role: "editor" } });
    await authApi.deleteToken("t/1");
    expect(calls.at(-1)?.key).toBe("DELETE /api/tokens/t%2F1");
    await authApi.audit();
    expect(calls.at(-1)?.key).toBe("GET /api/audit?limit=200");
    await authApi.audit(5);
    expect(calls.at(-1)?.key).toBe("GET /api/audit?limit=5");
  });

  it("uploads an extension file as raw bytes with its name in the query", async () => {
    const fetchMock = vi.fn(async (..._args: unknown[]) => ({ ok: true, status: 200, json: async () => ({}) }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    const file = new File(["x"], "my plugin.zip");
    await api.uploadExtension(file);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/extensions/upload?name=my%20plugin.zip");
    expect(init.method).toBe("POST");
    expect(init.headers).toEqual({ "Content-Type": "application/octet-stream" });
    expect(init.body).toBe(file);
  });
});

describe("call", () => {
  const reply = (status: number, body: unknown) => vi.stubGlobal("fetch", vi.fn(async () => ({ ok: status < 400, status, json: async () => body }) as Response));

  it("reports an expired session on a 401 it was not told to ignore", async () => {
    const onExpired = vi.fn();
    session.onExpired = onExpired;
    reply(401, { error: "sign in again" });
    await expect(request("/api/devices")).rejects.toThrow("sign in again");
    expect(onExpired).toHaveBeenCalledOnce();
  });

  it("does not treat a 401 from sign-in as an expired session", async () => {
    const onExpired = vi.fn();
    session.onExpired = onExpired;
    reply(401, { error: "wrong password" });
    await expect(authApi.login("ann", "wrong")).rejects.toMatchObject({ message: "wrong password", status: 401 });
    expect(onExpired).not.toHaveBeenCalled();
  });

  it("uses the fallback, then the status, when the answer has no error text", async () => {
    reply(500, null);
    await expect(call("/x", undefined, { fallback: "Upload failed" })).rejects.toThrow("Upload failed");
    await expect(call("/x")).rejects.toThrow("Request failed with status 500");
  });

  it("gives the fallback when the answer isn't JSON at all", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({ ok: false, status: 502, json: async () => { throw new SyntaxError("bad json"); } }) as unknown as Response));
    await expect(call("/x", undefined, { fallback: "Upload failed" })).rejects.toThrow("Upload failed");
  });

  it("returns the body of a good answer and keeps the status on errors", async () => {
    reply(200, { ok: 1 });
    await expect(call("/x")).resolves.toEqual({ ok: 1 });
    reply(403, { error: "no" });
    await expect(call("/x")).rejects.toMatchObject({ message: "no", status: 403 });
  });

  it("sends no body or headers for a GET", async () => {
    const fetchMock = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    await request("/api/devices");
    expect(fetchMock).toHaveBeenCalledWith("/api/devices", {});
  });

  it("sends JSON with a content type for a POST", async () => {
    const fetchMock = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }) as Response);
    vi.stubGlobal("fetch", fetchMock);
    await request("/api/x", { a: 1 });
    expect(fetchMock).toHaveBeenCalledWith("/api/x", { method: "POST", headers: { "Content-Type": "application/json" }, body: '{"a":1}' });
  });
});
