import type { ReactNode } from "react";
import { vi } from "vitest";
import { ConfirmProvider } from "@/components/ConfirmDialog";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AuthContext } from "@/lib/auth";
import type { Permission, Role } from "@/authApi";

export const PERMISSIONS: Record<Role, Permission[]> = {
  viewer: ["devices.read", "automations.read", "extensions.read"],
  operator: ["devices.read", "devices.write", "automations.read", "extensions.read"],
  editor: ["devices.read", "devices.write", "automations.read", "automations.edit", "automations.live", "extensions.read"],
  admin: ["devices.read", "devices.write", "automations.read", "automations.edit", "automations.live", "automations.approve", "extensions.read", "extensions.manage", "users.manage", "audit.read", "settings.manage"],
};

/** Stands in for a signed-in session with a role's permissions, for components that only ask can(). */
export function WithRole({ role = "admin", children }: { role?: Role; children: ReactNode }) {
  const permissions = PERMISSIONS[role];
  const value = {
    status: { setupRequired: false, user: { id: "u1", username: "ann", role }, permissions },
    expired: false, unreachable: false, welcome: false, setWelcome: () => {},
    can: (p: Permission) => permissions.includes(p),
    refresh: async () => {}, adopt: () => {}, signedOut: () => {},
  };
  return (
    <AuthContext.Provider value={value}>
      <ConfirmProvider><TooltipProvider>{children}</TooltipProvider></ConfirmProvider>
    </AuthContext.Provider>
  );
}

type Reply = { status?: number; body?: unknown };
export type Routes = Record<string, (body: unknown, url: URL) => Reply | undefined>;

/** A fake service: "METHOD /path" to a reply. Anything unlisted is a 404, and every call is recorded. */
export function mockService(routes: Routes) {
  const calls: { key: string; body: unknown }[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const key = `${init?.method ?? "GET"} ${url.pathname}`;
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ key, body });
    const reply = routes[key]?.(body, url) ?? { status: 404, body: { error: "not found" } };
    const status = reply.status ?? 200;
    return { ok: status < 400, status, json: async () => reply.body ?? null } as Response;
  });
  vi.stubGlobal("fetch", fn);
  return calls;
}
