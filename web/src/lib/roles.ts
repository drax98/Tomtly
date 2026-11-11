import { ROLES, type Role } from "@/authApi";
import { shortDate } from "@/lib/utils";

export const ROLE_INFO: Record<Role, { name: string; summary: string }> = {
  viewer: { name: "Viewer", summary: "Can look at everything, but can't change anything." },
  operator: { name: "Family", summary: "Can control devices: lights, heating, blinds, the vacuum." },
  editor: { name: "Editor", summary: "Can also write and change automations." },
  admin: { name: "Administrator", summary: "Everything, including people, add-ons, what automations may reach and the activity log." },
};

/** A person can only hand out roles up to their own. */
export const rolesUpTo = (role: Role) => ROLES.slice(0, ROLES.indexOf(role) + 1);

export function ago(time?: string | null): string {
  if (!time) return "Never";
  const minutes = Math.round((Date.now() - new Date(time).getTime()) / 60000);
  if (minutes < 1) return "Just now";
  if (minutes < 60) return `${minutes} min ago`;
  if (minutes < 1440) return `${Math.round(minutes / 60)} h ago`;
  return shortDate(time, time);
}
