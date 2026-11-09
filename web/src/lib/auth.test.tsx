import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { request, session } from "@/api";
import type { AuthStatus } from "@/authApi";
import { mockService, type Routes } from "@/test/helpers";
import { AuthProvider, useAuth, useUser } from "./auth";

const signedIn: AuthStatus = {
  setupRequired: false,
  user: { id: "u1", username: "ann", role: "operator" },
  permissions: ["devices.read", "devices.write"],
};

const wrapper = ({ children }: { children: ReactNode }) => <AuthProvider>{children}</AuthProvider>;

beforeEach(() => {
  session.expired = false;
  session.onExpired = () => {};
});
afterEach(() => {
  cleanup();
  session.onExpired = () => {};
  session.expired = false;
  vi.unstubAllGlobals();
});

const serve = (routes: Routes = {}) => mockService({ "GET /api/auth/status": () => ({ body: signedIn }), ...routes });

describe("AuthProvider", () => {
  it("loads who is signed in and what they may do", async () => {
    serve();
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.status).toBeNull();
    await waitFor(() => expect(result.current.status?.user?.username).toBe("ann"));
    expect(result.current.can("devices.write")).toBe(true);
    expect(result.current.can("users.manage")).toBe(false);
    expect(result.current.unreachable).toBe(false);
  });

  it("says the service is unreachable when the status can't be had, and recovers on refresh", async () => {
    let up = false;
    serve({ "GET /api/auth/status": () => (up ? { body: signedIn } : { status: 500, body: {} }) });
    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.unreachable).toBe(true));
    expect(result.current.status).toBeNull();
    up = true;
    await act(() => result.current.refresh());
    expect(result.current.unreachable).toBe(false);
    expect(result.current.status?.user?.username).toBe("ann");
  });

  it("marks the session expired when any call is refused as signed out, and clears it on adopt", async () => {
    serve({ "GET /api/devices": () => ({ status: 401, body: { error: "signed out" } }) });
    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.status).not.toBeNull());
    await expect(request("/api/devices")).rejects.toThrow("signed out");
    await waitFor(() => expect(result.current.expired).toBe(true));
    expect(session.expired).toBe(true);
    act(() => result.current.adopt(signedIn));
    expect(result.current.expired).toBe(false);
    expect(result.current.unreachable).toBe(false);
    expect(session.expired).toBe(false);
  });

  it("forgets the user and permissions on sign-out but keeps the page's status shape", async () => {
    serve();
    const { result } = renderHook(() => useAuth(), { wrapper });
    await waitFor(() => expect(result.current.status?.user).not.toBeNull());
    act(() => result.current.signedOut());
    expect(result.current.status?.user).toBeNull();
    expect(result.current.status?.permissions).toEqual([]);
    expect(result.current.can("devices.read")).toBe(false);
    expect(result.current.expired).toBe(false);
  });

  it("keeps a welcome until someone turns it off", async () => {
    serve();
    const { result } = renderHook(() => useAuth(), { wrapper });
    expect(result.current.welcome).toBe(false);
    act(() => result.current.setWelcome(true));
    expect(result.current.welcome).toBe(true);
    act(() => result.current.setWelcome(false));
    expect(result.current.welcome).toBe(false);
  });

  it("needs a provider, and a signed-in user below the sign-in gate", () => {
    expect(() => renderHook(() => useAuth())).toThrow("useAuth needs an AuthProvider");
    serve({ "GET /api/auth/status": () => ({ body: { ...signedIn, user: null, permissions: [] } }) });
    expect(() => renderHook(() => useUser(), { wrapper })).toThrow("No signed-in user");
  });
});
