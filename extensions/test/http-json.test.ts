import { describe, expect, it } from "vitest";
import { loadExtension, makeHost } from "./harness";

const load = () => loadExtension(() => import("../http-json/extension.ts"));

describe("http-json", () => {
  it("fetches the path over http and returns the JSON, with no headers when there's no token", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "prices.local:9000", path: "/today", https: false }, fetch: () => ({ body: { cheap: [1, 2] } }) });
    expect(def.data.fetch(host.ctx)).toEqual({ cheap: [1, 2] });
    expect(host.fetches).toEqual([{ url: "http://prices.local:9000/today", options: { headers: {} } }]);
  });

  it("uses https and a bearer token when they're set", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "api.example.com", path: "/x", https: true, token: "T0K" }, fetch: () => ({ body: [] }) });
    def.data.fetch(host.ctx);
    expect(host.fetches).toEqual([{ url: "https://api.example.com/x", options: { headers: { Authorization: "Bearer T0K" } } }]);
  });

  it("throws the status when the service doesn't answer 200", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h", path: "/" }, fetch: () => ({ status: 404 }) });
    expect(() => def.data.fetch(host.ctx)).toThrow("the service answered 404");
  });

  it("throws when the body isn't JSON", async () => {
    const def = await load();
    const host = makeHost({ settings: { host: "h", path: "/" }, fetch: () => ({ body: "<html>" }) });
    expect(() => def.data.fetch(host.ctx)).toThrow();
  });

  it("declares a five-minute data interval and the host it may reach", async () => {
    const def = await load();
    expect(def.data.everySeconds).toBe(300);
    expect(def.access.network).toEqual(["{host}"]);
  });
});
