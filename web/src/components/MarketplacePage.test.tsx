import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExtensionStatus, Marketplace, MarketEntry } from "@/api";
import { mockService, WithRole, type Routes } from "@/test/helpers";
import { IntegrationsPage } from "./IntegrationsPage";
import { MarketplacePage } from "./MarketplacePage";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const entry = (over: Partial<MarketEntry>): MarketEntry => ({
  id: "tides", name: "Tides", description: "Tide times", kind: "plugin", version: "1.0.0", author: "Ann", provides: ["data"], access: ["Internet: tides.example.com"],
  testedOn: "Simulator only", approvedOn: "2026-10-30", size: 10, installed: false, updateAvailable: false, sha256: "3f2a91c7d4e08b16".repeat(4), repository: "https://example.com/ann/tides", ...over,
});
const hue = entry({ id: "hue", name: "Hue bridge", kind: "integration", provides: ["devices"], description: "Lights", access: [] });
const weather = entry({ id: "weather", name: "Weather", description: "Forecast", installed: true, installedVersion: "1.0.0", installedSource: "github:a/b@v1", updateAvailable: true, version: "1.1.0" });
const tides = entry({});
const catalog = (over: Partial<Marketplace> = {}): Marketplace => ({ configured: true, reachable: true, verified: true, entries: [tides, hue, weather], ...over });

const found: ExtensionStatus = {
  source: "market:tides@1.0.0#" + "a".repeat(64), id: "tides", name: "Tides", version: "1.0.0", access: ["Internet: tides.example.com"], provides: ["data"], running: false,
  settings: [{ key: "city", label: "City", type: "string", required: true, set: false }],
};

const SITE = "https://market.example.com";
const serve = (market: Marketplace, extra: Routes = {}) => mockService({
  "GET /api/marketplace": () => ({ body: { site: SITE, ...market } }),
  "GET /api/marketplace/tides/reviews": () => ({ body: { reviews: [], average: 0, count: 0 } }),
  "GET /api/marketplace/weather/reviews": () => ({ body: { reviews: [], average: 0, count: 0 } }),
  ...extra,
});
/** Get on the marketplace's website opens a listing here, so that's what most tests show. */
const show = (role: "admin" | "viewer" = "admin", open = "tides") => {
  window.history.replaceState(null, "", open ? `/marketplace/${open}` : "/marketplace");
  return render(<WithRole role={role}><MarketplacePage open={open} /></WithRole>);
};
const noReviews = { "GET /api/marketplace/tides/reviews": () => ({ body: { reviews: [], average: 0, count: 0 } }) };
const asset = (name: string) => ({ name, type: "image/png", sha256: "c".repeat(64) });

afterEach(cleanup);
beforeEach(() => vi.unstubAllGlobals());

describe("MarketplacePage", () => {
  it("never calls something else with the same id the marketplace install", async () => {
    serve(catalog({ entries: [entry({ differentCopy: true })] }));
    show();
    const card = within(await screen.findByTestId("market-detail-tides"));
    expect(card.getByText("A different copy is installed")).toBeTruthy();
    expect(card.queryByText(/^Installed/)).toBeNull();
    expect(card.queryByRole("button", { name: /Install/ })).toBeNull();
  });

  it("says why a suspended listing can't be installed", async () => {
    serve(catalog({ entries: [entry({ suspended: "A report came in" })] }));
    show();
    const card = within(await screen.findByTestId("market-detail-tides"));
    expect(card.getByText(/A report came in/)).toBeTruthy();
    expect(card.queryByRole("button", { name: /Install/ })).toBeNull();
  });

  it("says what is wrong when the list can't be trusted", async () => {
    serve(catalog({ verified: false, entries: [], error: "the marketplace can't be verified: MARKETPLACE_APPROVAL_PUBLIC_KEYS isn't set" }));
    show();
    expect(await screen.findByRole("heading", { name: "Can't verify the marketplace" })).toBeTruthy();
    expect(screen.getByText(/MARKETPLACE_APPROVAL_PUBLIC_KEYS isn't set/)).toBeTruthy();
  });

  it("says on a listing's page how it was tested, what it is and what it needs", async () => {
    serve(catalog({ entries: [entry({ provides: ["data", "widgets"] })] }));
    show();
    const info = within(await screen.findByRole("region", { name: "Information" }));
    expect(info.getByText("Tested on").nextElementSibling?.textContent).toBe("Simulator only");
    expect(info.getByText("Internet: tides.example.com")).toBeTruthy();
    expect(info.getByText("Plugin, with widgets for Home")).toBeTruthy();
  });

  it("a tag searches the marketplace's website", async () => {
    const opened = vi.fn();
    vi.stubGlobal("open", opened);
    serve(catalog({ entries: [entry({ tags: ["Sea"] })] }));
    show();
    const detail = within(await screen.findByTestId("market-detail-tides"));
    fireEvent.click(detail.getByRole("button", { name: "Show everything tagged Sea" }));
    expect(opened).toHaveBeenCalledWith(`${SITE}/?q=Sea`, "_blank", "noopener,noreferrer");
  });

  it("without a listing, points to the website and lists updates for what's installed", async () => {
    const opened = vi.fn();
    vi.stubGlobal("open", opened);
    serve(catalog());
    show("admin", "");
    fireEvent.click(await screen.findByRole("button", { name: "Open the marketplace" }));
    expect(opened).toHaveBeenCalledWith(`${SITE}/`, "_blank", "noopener,noreferrer");
    const updates = within(screen.getByRole("region", { name: "Updates" }));
    expect(updates.getByText("1.0.0 → 1.1.0")).toBeTruthy();
    expect(updates.queryByText("Tides")).toBeNull();
    fireEvent.click(updates.getByRole("link", { name: "Weather" }));
    expect(window.location.pathname).toBe("/marketplace/weather");
  });

  it("lists what each version on the way to an update changes", async () => {
    serve(catalog({ entries: [entry({ installed: true, installedVersion: "1.0.0", installedSource: "market:tides@1.0.0#a", updateAvailable: true, version: "1.2.0", upgradeSteps: [{ version: "1.1.0", note: "New units." }, { version: "1.2.0", note: "Needs a token.", ask: ["token"] }] })] }), noReviews);
    show("admin", "tides");
    const card = within(await screen.findByTestId("market-detail-tides"));
    expect(card.getByRole("list", { name: "What changes" }).textContent).toContain("1.1.0: New units.");
    expect(card.getByRole("button", { name: "Update to 1.2.0" })).toBeTruthy();
  });

  it("installs through the second step, then points to where it appears", async () => {
    const calls = serve(catalog(), {
      "POST /api/marketplace/inspect": () => ({ body: found }),
      "POST /api/extensions": () => ({ body: { ...found, running: true } }),
    });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Install Tides" }));
    await screen.findByText("It needs this access:");
    expect(calls.find((c) => c.key === "POST /api/marketplace/inspect")?.body).toEqual({ id: "tides" });
    expect(calls.some((c) => c.key === "POST /api/extensions")).toBe(false);
    fireEvent.change(screen.getByLabelText("City"), { target: { value: "Oslo" } });
    fireEvent.click(screen.getByRole("button", { name: "Allow this access and add Tides" }));
    await screen.findByText("Tides is installed");
    expect(calls.find((c) => c.key === "POST /api/extensions")?.body).toEqual({ source: found.source, settings: { city: "Oslo" }, access: found.access });
    expect(screen.getByRole("link", { name: "Go to Plugins" }).getAttribute("href")).toBe("/plugins");
  });

  it("says why a package was refused", async () => {
    serve(catalog(), { "POST /api/marketplace/inspect": () => ({ status: 400, body: { error: "the download isn't what the marketplace listed" } }) });
    show();
    fireEvent.click(await screen.findByRole("button", { name: "Install Tides" }));
    await screen.findByText("the download isn't what the marketplace listed");
    expect(screen.queryByRole("button", { name: /Allow this access/ })).toBeNull();
  });

  it("updates after showing the new access", async () => {
    const calls = serve(catalog(), {
      "POST /api/marketplace/inspect": () => ({ body: { ...found, id: "weather", name: "Weather", version: "1.1.0", source: "market:weather@1.1.0#" + "b".repeat(64), access: ["Internet: new.example.com"] } }),
      "GET /api/extensions": () => ({ body: [{ ...found, source: "github:a/b@v1", access: ["Internet: old.example.com"] }] }),
      "POST /api/extensions/replace": () => ({ body: found }),
    });
    show("admin", "weather");
    fireEvent.click(await screen.findByRole("button", { name: "Update to 1.1.0" }));
    await screen.findByText(/asks for access the old one didn't have/);
    expect(screen.getByText("Internet: new.example.com")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(calls.find((c) => c.key === "POST /api/extensions/replace")?.body).toEqual({ source: "github:a/b@v1", newSource: "market:weather@1.1.0#" + "b".repeat(64), access: ["Internet: new.example.com"] }));
  });

  describe("an update that needs something", () => {
    const pat = { key: "pat", label: "Personal access token", type: "secret" as const, required: true, help: "From your account page", set: false };
    const token = { key: "token", label: "Old token", type: "secret" as const, required: true, set: true };
    const next: ExtensionStatus = {
      ...found, id: "weather", name: "Weather", version: "1.1.0", source: "market:weather@1.1.0#" + "b".repeat(64), access: found.access, settings: [pat, token],
      upgrade: { from: "1.0.0", to: "1.1.0", steps: [{ version: "1.1.0", note: "The service now needs a token." }], ask: [pat, token] },
    };
    const needing = entry({ ...weather, upgradeSteps: [{ version: "1.1.0", note: "The service now needs a token.", ask: ["pat", "token"] }] });
    const open = async (extra: Routes = {}) => {
      const calls = serve(catalog({ entries: [needing] }), {
        "POST /api/marketplace/inspect": () => ({ body: next }),
        "GET /api/extensions": () => ({ body: [{ ...found, source: "github:a/b@v1" }] }),
        ...extra,
      });
      show("admin", "weather");
      fireEvent.click(await screen.findByRole("button", { name: "Update to 1.1.0" }));
      await screen.findByText("The service now needs a token.", { selector: "li" });
      return calls;
    };
    const button = () => screen.getByRole("button", { name: "Update" }) as HTMLButtonElement;

    it("hints on its page, by count until the labels are known and by name after", async () => {
      serve(catalog({ entries: [needing] }), { "POST /api/marketplace/inspect": () => ({ body: next }), "GET /api/extensions": () => ({ body: [] }), "GET /api/marketplace/weather/reviews": () => ({ body: { reviews: [], average: 0, count: 0 } }) });
      show("admin", "weather");
      const card = within(await screen.findByTestId("market-detail-weather"));
      expect(card.getByText("This update needs 2 settings from you.")).toBeTruthy();
      expect(card.getByText(/not what was verified/)).toBeTruthy();
      expect(card.getByRole("link", { name: "https://example.com/ann/tides" }).getAttribute("href")).toBe("https://example.com/ann/tides");
      expect(card.getByText("3f2a91c7d4e0…")).toBeTruthy();
      fireEvent.click(card.getByRole("button", { name: "Update to 1.1.0" }));
      await screen.findByLabelText("Personal access token");
      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      await screen.findByText("This update needs: Personal access token, Old token");
    });

    it("shows no hint when the update asks for nothing", async () => {
      serve(catalog());
      show("admin", "weather");
      expect(within(await screen.findByTestId("market-detail-weather")).queryByText(/This update needs/)).toBeNull();
    });

    it("shows the steps and a form, and waits until it's filled", async () => {
      await open();
      expect(screen.getByText("Version 1.1.0:")).toBeTruthy();
      const field = screen.getByLabelText("Personal access token") as HTMLInputElement;
      expect(field.type).toBe("password");
      expect(screen.getByText("From your account page")).toBeTruthy();
      const kept = screen.getByLabelText("Old token") as HTMLInputElement;
      expect(kept.value).toBe("");
      expect(kept.placeholder).toBe("Leave blank to keep it");
      expect(button().disabled).toBe(true);
      fireEvent.change(field, { target: { value: "   " } });
      expect(button().disabled).toBe(true);
      fireEvent.change(field, { target: { value: "ghp_x" } });
      expect(button().disabled).toBe(false);
    });

    it("sends what was typed and leaves out a blank kept secret", async () => {
      const calls = await open({ "POST /api/extensions/replace": () => ({ body: next }) });
      fireEvent.change(screen.getByLabelText("Personal access token"), { target: { value: "ghp_x" } });
      fireEvent.click(button());
      await waitFor(() => expect(calls.find((c) => c.key === "POST /api/extensions/replace")?.body).toEqual({ source: "github:a/b@v1", newSource: next.source, settings: { pat: "ghp_x" }, access: next.access }));
      await waitFor(() => expect(screen.queryByLabelText("Personal access token")).toBeNull());
    });

    it("keeps the form open with what was typed when the server refuses", async () => {
      let refuse = true;
      const calls = await open({ "POST /api/extensions/replace": () => (refuse ? { status: 400, body: { error: 'the setting "pat" is required' } } : { body: next }) });
      const field = screen.getByLabelText("Personal access token") as HTMLInputElement;
      fireEvent.change(field, { target: { value: "ghp_x" } });
      fireEvent.click(button());
      expect((await screen.findByRole("alert")).textContent).toBe('the setting "pat" is required');
      expect((screen.getByLabelText("Personal access token") as HTMLInputElement).value).toBe("ghp_x");
      refuse = false;
      fireEvent.click(button());
      await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
      expect(calls.filter((c) => c.key === "POST /api/extensions/replace").length).toBe(2);
    });
  });

  it("lets a viewer look but not install", async () => {
    serve(catalog());
    show("viewer");
    const install = await screen.findByRole("button", { name: "Install Tides" });
    expect((install as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/Installing needs an administrator/)).toBeTruthy();
  });

  it.each([
    ["isn't set up", catalog({ configured: false, reachable: false, verified: false, entries: [] }), "The marketplace isn't set up"],
    ["can't be reached", catalog({ reachable: false, verified: false, entries: [] }), "Can't reach the marketplace"],
    ["can't be verified", catalog({ verified: false, entries: [] }), "Can't verify the marketplace"],
  ])("explains when the marketplace %s", async (_name, market, title) => {
    serve(market);
    show();
    await screen.findByText(title);
    expect(screen.queryByRole("searchbox")).toBeNull();
  });

  it("retries when the marketplace can't be reached", async () => {
    let up = false;
    mockService({
      "GET /api/marketplace": () => ({ body: up ? catalog() : catalog({ reachable: false, verified: false, entries: [] }) }),
      "GET /api/marketplace/tides/reviews": () => ({ body: { reviews: [], average: 0, count: 0 } }),
    });
    show();
    up = true;
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await screen.findByTestId("market-detail-tides");
  });

  it("says so when Tomtly itself doesn't answer", async () => {
    mockService({ "GET /api/marketplace": () => ({ status: 500, body: { error: "boom" } }) });
    show();
    await screen.findByText("Can't reach Tomtly");
  });

  it("is linked from the Integrations page", async () => {
    mockService({ "GET /api/integrations": () => ({ body: [] }) });
    render(<WithRole><IntegrationsPage go={() => {}} /></WithRole>);
    fireEvent.keyDown(screen.getByRole("button", { name: "Add integration" }), { key: "Enter" });
    expect((await screen.findByRole("menuitem", { name: /From the marketplace/ })).getAttribute("href")).toBe("/marketplace");
  });

  it("shows an icon, or a neutral stand-in, only ever from Tomtly", async () => {
    serve(catalog({ entries: [entry({ icon: asset("icon.png"), rating: { average: 4.5, count: 12 } }), entry({ id: "plain", name: "Plain" })] }));
    const { container } = show();
    const detail = await screen.findByTestId("market-detail-tides");
    expect(detail.querySelector("img")?.getAttribute("src")).toMatch(/^\/api\/marketplace\/tides\/assets\/icon\.png\?v=c+$/);
    expect(within(detail).getAllByRole("img", { name: "4.5 out of 5" }).length).toBeGreaterThan(0);
    // Nothing on the page may load from anywhere but Tomtly.
    for (const img of container.querySelectorAll("img")) expect(img.getAttribute("src")).toMatch(/^\/api\//);
    cleanup();
    show("admin", "plain");
    expect((await screen.findByTestId("market-detail-plain")).querySelector("img")).toBeNull();
  });

  it("shows a listing on its own, with a way back and the actions", async () => {
    serve(catalog({ entries: [entry({ icon: asset("icon.png"), screenshots: [asset("screenshot-1.png"), asset("screenshot-2.png")] }), hue] }), {
      "GET /api/marketplace/tides/reviews": () => ({ body: { reviews: [{ id: "a1", rating: 5, text: "Lovely", name: "Ola", at: "2026-10-28T10:00:00Z" }], average: 4.5, count: 1 } }),
    });
    show("admin", "tides");
    const detail = within(await screen.findByTestId("market-detail-tides"));
    expect(detail.getByRole("heading", { name: "Tides" })).toBeTruthy();
    expect(detail.getByText("Tide times")).toBeTruthy();
    expect(detail.getByText("Provider").nextElementSibling?.textContent).toBe("Ann");
    expect(detail.getByText("Internet: tides.example.com")).toBeTruthy();
    expect(detail.getByAltText("Screenshot 2 of Tides").getAttribute("src")).toContain("/api/marketplace/tides/assets/screenshot-2.png");
    expect(detail.getByRole("button", { name: "Install Tides" })).toBeTruthy();
    expect(await detail.findByText("Lovely")).toBeTruthy();
    expect(detail.getByRole("img", { name: "4.5 out of 5" })).toBeTruthy();
    expect(screen.queryByTestId("market-hue")).toBeNull();
    expect(screen.queryByRole("searchbox")).toBeNull();
    fireEvent.click(screen.getByRole("link", { name: "Marketplace" }));
    expect(window.location.pathname).toBe("/marketplace");
  });

  it("says plainly when a listing is gone", async () => {
    serve(catalog());
    show("admin", "gone");
    expect(await screen.findByText("Not in the marketplace any more")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Marketplace" })).toBeTruthy();
  });

  it("keeps working when reviews can't load", async () => {
    serve(catalog({ entries: [entry({ rating: { average: 3, count: 2 } })] }), { "GET /api/marketplace/tides/reviews": () => ({ status: 502, body: { error: "down" } }) });
    show("admin", "tides");
    expect(await screen.findByText(/reviews can't load/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Install Tides" })).toBeTruthy();
    expect(screen.getAllByRole("img", { name: "3 out of 5" }).length).toBeGreaterThan(0);
  });

  it("shows reviews as plain text, with no links", async () => {
    const hostile = "<a href='https://evil.example'>click</a> https://evil.example <img src=x onerror=alert(1)>";
    serve(catalog(), { "GET /api/marketplace/tides/reviews": () => ({ body: { reviews: [{ id: "a1", rating: 1, text: hostile, name: "<b>Bob</b>", at: "nonsense" }], average: 1, count: 1 } }) });
    show("viewer", "tides");
    const detail = await screen.findByTestId("market-detail-tides");
    expect(await within(detail).findByText(hostile)).toBeTruthy();
    expect(within(detail).getByText("<b>Bob</b>")).toBeTruthy();
    expect(detail.querySelector("img[src='x']")).toBeNull();
    expect(detail.querySelector("a[href*='evil']")).toBeNull();
  });

  it("copes with a rating out of range all the way to the label", async () => {
    serve(catalog({ entries: [entry({ rating: { average: 99, count: 1 } })] }));
    show();
    expect((await screen.findAllByRole("img", { name: "5 out of 5" })).length).toBeGreaterThan(0);
  });

  it("tells people a review is public, starts with no name, and posts only what they typed", async () => {
    const posted = serve(catalog(), { ...noReviews, "POST /api/marketplace/tides/reviews": () => ({ body: { ok: true } }) });
    show("admin", "tides");
    await screen.findByTestId("market-detail-tides");
    expect(screen.getByText(/are public and go to the marketplace/)).toBeTruthy();
    const name = screen.getByLabelText("Name to show (optional)") as HTMLInputElement;
    expect(name.value).toBe("");
    const post = screen.getByRole("button", { name: "Post publicly" }) as HTMLButtonElement;
    expect(post.disabled).toBe(true);
    fireEvent.click(screen.getByRole("radio", { name: "4" }));
    fireEvent.change(screen.getByLabelText("Your review (optional)"), { target: { value: "Works" } });
    fireEvent.click(post);
    await waitFor(() => expect(posted.some((c) => c.key === "POST /api/marketplace/tides/reviews")).toBe(true));
    expect(posted.find((c) => c.key === "POST /api/marketplace/tides/reviews")?.body).toEqual({ rating: 4, text: "Works", name: "" });
    await waitFor(() => expect((screen.getByLabelText("Your review (optional)") as HTMLTextAreaElement).value).toBe(""));
  });

  it("shows why a review wasn't posted and keeps what was typed", async () => {
    serve(catalog(), { ...noReviews, "POST /api/marketplace/tides/reviews": () => ({ status: 429, body: { error: "Too many reviews from here." } }) });
    show("admin", "tides");
    await screen.findByTestId("market-detail-tides");
    fireEvent.click(screen.getByRole("radio", { name: "2" }));
    fireEvent.change(screen.getByLabelText("Your review (optional)"), { target: { value: "Keep me" } });
    fireEvent.click(screen.getByRole("button", { name: "Post publicly" }));
    expect(await screen.findByText("Too many reviews from here.")).toBeTruthy();
    expect((screen.getByLabelText("Your review (optional)") as HTMLTextAreaElement).value).toBe("Keep me");
  });

  it("offers no review form without permission", async () => {
    serve(catalog(), noReviews);
    show("viewer", "tides");
    await screen.findByTestId("market-detail-tides");
    expect(screen.queryByRole("button", { name: "Post publicly" })).toBeNull();
  });

  it("pages through reviews", async () => {
    const offsets: (string | null)[] = [];
    const page = (id: string) => ({ id, rating: 3, text: id, name: "", at: "2026-10-28T10:00:00Z" });
    serve(catalog(), {
      "GET /api/marketplace/tides/reviews": (_body, url) => {
        offsets.push(url.searchParams.get("offset"));
        return { body: { reviews: url.searchParams.get("offset") === "0" ? [page("a1"), page("a2")] : [page("a3")], average: 3, count: 3 } };
      },
    });
    show("viewer", "tides");
    await screen.findByText("a2");
    fireEvent.click(screen.getByRole("button", { name: "Show more" }));
    await screen.findByText("a3");
    expect(offsets).toEqual(["0", "2"]);
    expect(screen.getByText("a1")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
  });
});
