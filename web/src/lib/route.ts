import { useEffect, useState } from "react";

export const PAGES = ["home", "devices", "rooms", "automations", "integrations", "plugins", "marketplace", "users", "audit", "tokens", "settings"] as const;
export type Page = (typeof PAGES)[number];

export interface Route {
  page: Page;
  /** What follows the first slash: a script's name. */
  sub: string;
}

/** Reads a path, or the page names kept in memory and in old links that used a hash. */
export function parseRoute(path: string): Route | null {
  const [first, ...rest] = path.replace(/^[/#]+/, "").split("/");
  const page = PAGES.find((p) => p === first);
  return page ? { page, sub: decodeURIComponent(rest.join("/")) } : null;
}

const LAST = "lastPage";

function pathOf(page: Page, sub: string): string {
  return sub ? `/${page}/${encodeURIComponent(sub)}` : `/${page}`;
}

function initial(): Route {
  const fromUrl = parseRoute(window.location.pathname) ?? parseRoute(window.location.hash);
  if (fromUrl) return fromUrl;
  try {
    // Only a page name is kept, never anything a person typed.
    const last = parseRoute(localStorage.getItem(LAST) ?? "");
    if (last) return { page: last.page, sub: "" };
  } catch {
    // Remembering the page is only a convenience.
  }
  return { page: "home", sub: "" };
}

/** Moves to a page without reloading it. search, such as "?q=hue", is kept in the address for the page to read. */
export function navigate(page: Page, sub = "", search = "") {
  const to = pathOf(page, sub) + search;
  if (window.location.pathname + window.location.search === to) return;
  window.history.pushState(null, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

/** The URL path is the source of truth, so refresh, back and shared links all land on the same page. */
export function useRoute(): [Route, (page: Page, sub?: string) => void] {
  const [route, setRoute] = useState<Route>(initial);

  useEffect(() => {
    const onChange = () => setRoute(parseRoute(window.location.pathname) ?? { page: "home", sub: "" });
    window.addEventListener("popstate", onChange);
    return () => window.removeEventListener("popstate", onChange);
  }, []);

  useEffect(() => {
    // Make the URL match what's shown, e.g. when the page came from memory or an old link.
    const want = pathOf(route.page, route.sub);
    if (window.location.pathname + window.location.hash !== want) window.history.replaceState(null, "", want);
    try {
      localStorage.setItem(LAST, route.page);
    } catch {
      // Same: the page works without it.
    }
  }, [route]);

  return [route, navigate];
}
