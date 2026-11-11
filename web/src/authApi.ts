import { request } from "@/api";

export const ROLES = ["viewer", "operator", "editor", "admin"] as const;
export type Role = (typeof ROLES)[number];

export type Permission =
  | "devices.read" | "devices.write"
  | "automations.read" | "automations.edit" | "automations.live" | "automations.approve"
  | "extensions.read" | "extensions.manage"
  | "users.manage" | "audit.read" | "settings.manage";

export interface User {
  id: string;
  username: string;
  role: Role;
}

export interface AuthStatus {
  setupRequired: boolean;
  /** Setting up from here takes the one-time code from the service's logs. */
  setupTokenRequired?: boolean;
  user: User | null;
  permissions: Permission[];
}

export interface ManagedUser extends User {
  disabled: boolean;
  created: string;
}

export interface ApiToken {
  id: string;
  name: string;
  role: Role;
  owner: string;
  created: string;
  lastUsed: string | null;
}

export interface AuditEntry {
  time: string;
  user: string;
  action: string;
  detail: string;
  ip: string;
}

export const MIN_PASSWORD = 12;

// Sign-in and setup answer 401 for a wrong password, which must not look like an expired session.
const open = { authed: false };

export const authApi = {
  status: () => request<AuthStatus>("/api/auth/status", undefined, "GET", open),
  setup: (username: string, password: string, setupToken?: string) => request<AuthStatus>("/api/auth/setup", { username, password, setupToken }, "POST", open),
  login: (username: string, password: string) => request<AuthStatus>("/api/auth/login", { username, password }, "POST", open),
  logout: () => request<unknown>("/api/auth/logout", {}),
  changePassword: (current: string, next: string, keepTokens: boolean) => request<unknown>("/api/auth/password", { current, new: next, keepTokens }),
  users: () => request<ManagedUser[]>("/api/users"),
  createUser: (username: string, password: string, role: Role) => request<ManagedUser>("/api/users", { username, password, role }),
  updateUser: (id: string, change: { role?: Role; disabled?: boolean; password?: string }) =>
    request<ManagedUser>(`/api/users/${encodeURIComponent(id)}`, change, "PATCH"),
  deleteUser: (id: string) => request<unknown>(`/api/users/${encodeURIComponent(id)}`, undefined, "DELETE"),
  tokens: () => request<ApiToken[]>("/api/tokens"),
  createToken: (name: string, role: Role) => request<ApiToken & { token: string }>("/api/tokens", { name, role }),
  deleteToken: (id: string) => request<unknown>(`/api/tokens/${encodeURIComponent(id)}`, undefined, "DELETE"),
  audit: (limit = 200) => request<AuditEntry[]>(`/api/audit?limit=${limit}`),
};
