import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** messageOf is what to show for a failure: its own message when it has one, else the fallback. */
export const messageOf = (e: unknown, fallback: string) => (e instanceof Error ? e.message : fallback);

/** shortDate is a day like "5 Nov 2026", or fallback when the value isn't a date. */
export const shortDate = (value: string, fallback: string) => {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? fallback : date.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
};
