import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { navigate, parseRoute, useRoute } from "./route";

beforeEach(() => {
  localStorage.clear();
  window.history.replaceState(null, "", "/");
});
afterEach(() => vi.restoreAllMocks());

describe("parseRoute", () => {
  it("reads the page from the path, with a slash or a hash in front", () => {
    expect(parseRoute("/devices")).toEqual({ page: "devices", sub: "" });
    expect(parseRoute("#/audit")).toEqual({ page: "audit", sub: "" });
    expect(parseRoute("//tokens")).toEqual({ page: "tokens", sub: "" });
  });

  it("gives what follows the page as the sub, decoded", () => {
    expect(parseRoute("/automations/heat%20pump")).toEqual({ page: "automations", sub: "heat pump" });
    expect(parseRoute("/plugins/a%2Fb")).toEqual({ page: "plugins", sub: "a/b" });
  });

  it("keeps the sub-path's own slashes and ignores unknown pages", () => {
    expect(parseRoute("automations/x/y")).toEqual({ page: "automations", sub: "x/y" });
    expect(parseRoute("/nope")).toBeNull();
    expect(parseRoute("")).toBeNull();
  });
});

describe("navigate", () => {
  it("pushes the page into the URL and tells the listeners", () => {
    const push = vi.spyOn(window.history, "pushState");
    const seen = vi.fn();
    window.addEventListener("popstate", seen);
    navigate("automations", "heating");
    expect(push).toHaveBeenCalledWith(null, "", "/automations/heating");
    expect(seen).toHaveBeenCalledOnce();
    window.removeEventListener("popstate", seen);
  });

  it("does nothing when the page is already the one in the URL", () => {
    navigate("users");
    const push = vi.spyOn(window.history, "pushState");
    const seen = vi.fn();
    window.addEventListener("popstate", seen);
    navigate("users");
    expect(push).not.toHaveBeenCalled();
    expect(seen).not.toHaveBeenCalled();
    window.removeEventListener("popstate", seen);
  });
});

describe("useRoute", () => {
  it("starts on the page in the URL, over the one remembered", () => {
    localStorage.setItem("lastPage", "audit");
    window.history.replaceState(null, "", "/tokens");
    const { result } = renderHook(() => useRoute());
    expect(result.current[0]).toEqual({ page: "tokens", sub: "" });
  });

  it("starts on the remembered page when the URL names none", () => {
    localStorage.setItem("lastPage", "audit");
    const { result } = renderHook(() => useRoute());
    expect(result.current[0]).toEqual({ page: "audit", sub: "" });
    expect(window.location.pathname).toBe("/audit");
  });

  it("falls back to home when nothing is remembered or the remembered name is junk", () => {
    localStorage.setItem("lastPage", "https://evil.example");
    const { result } = renderHook(() => useRoute());
    expect(result.current[0]).toEqual({ page: "home", sub: "" });
    expect(window.location.pathname).toBe("/home");
  });

  it("remembers the page it moves to and follows the URL on back", () => {
    const { result } = renderHook(() => useRoute());
    act(() => result.current[1]("tokens"));
    expect(result.current[0]).toEqual({ page: "tokens", sub: "" });
    expect(localStorage.getItem("lastPage")).toBe("tokens");
    expect(window.location.pathname).toBe("/tokens");

    act(() => {
      window.history.pushState(null, "", "/audit");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(result.current[0]).toEqual({ page: "audit", sub: "" });
  });

  it("stops listening to back navigation once unmounted", () => {
    const { result, unmount } = renderHook(() => useRoute());
    unmount();
    window.history.pushState(null, "", "/users");
    window.dispatchEvent(new PopStateEvent("popstate"));
    expect(result.current[0]).toEqual({ page: "home", sub: "" });
  });
});
