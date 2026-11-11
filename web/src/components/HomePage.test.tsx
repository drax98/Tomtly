import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { AutomationStatus, DataPoint, Device, ExtensionStatus, RenderedWidget } from "@/api";
import { mockService, WithRole } from "@/test/helpers";
import { CLEARED_FOR_MS, HomePage, problems, sortCleared } from "./HomePage";

const dp = (code: string, value: number | boolean | null, over: Partial<DataPoint> = {}): DataPoint => ({ dp: code, code, type: "Integer", values: {}, scale: 0, writable: false, value, ...over });
const device = (id: string, name: string, over: Partial<Device> = {}): Device => ({ id, adapter: "tuya", name, kind: "light", category: "", product: "", online: true, dps: [], ...over });
const ext = (over: Partial<ExtensionStatus>): ExtensionStatus => ({ source: "./extensions/x", name: "X", access: [], provides: ["devices"], running: true, settings: [], ...over } as ExtensionStatus);

const heater = device("h", "Radiator", { kind: "climate", dps: [dp("temp_current", 19.5, { values: { unit: "°C" } }), dp("temp_set", 21, { values: { unit: "°C" } }), dp("heating_state", true, { type: "Boolean" })] });
const sensor = device("s", "Door", { dps: [dp("battery_percentage", 12)] });
const gone = device("g", "Plug", { online: false });

afterEach(cleanup);

describe("problems", () => {
  it("is empty on a good day", () => {
    expect(problems([device("a", "A")], [ext({})], { scripts: [{ name: "heating", live: true, access: [] }], events: [] })).toEqual([]);
  });

  it("lists what needs someone, the serious ones first", () => {
    const now = Date.parse("2026-11-04T12:00:00Z");
    const list = problems(
      [sensor, gone, device("f", "Fan", { error: "the device refused" })],
      [ext({ name: "Tuya", needsApproval: true }), ext({ name: "Gree", violations: [{ time: "", extension: "gree", kind: "network", detail: "evil.example" }] }), ext({ name: "Hue", marketUpdate: { version: "1.1.0" } })],
      { scripts: [{ name: "lights", live: false, access: [], error: "boom" }], events: [
        { time: "2026-11-04T11:00:00Z", script: "heating", kind: "mismatch", message: "Radiator: script wants this" },
        { time: "2026-11-01T11:00:00Z", script: "old", kind: "mismatch", message: "long ago" },
      ] },
      now,
    ).map((p) => `${p.tone}: ${p.text}`);
    expect(list.slice(0, 3)).toEqual(["bad: Gree tried something it isn't allowed to", "bad: The lights automation doesn't run", "bad: A command to Fan failed"]);
    expect(list).toContain("warn: Tuya is waiting for your OK");
    expect(list).toContain("warn: Hue 1.1.0 is available");
    expect(list).toContain("warn: Plug is offline");
    expect(list).toContain("warn: Door's battery is low");
    expect(list).toContain("warn: A device isn't doing what heating wants");
    expect(list.join()).not.toContain("old");
  });
});

describe("clearing what needs you", () => {
  const loaded = { devices: true, extensions: true, automations: true };
  const now = Date.parse("2026-11-04T12:00:00Z");
  const auto = (time: string): AutomationStatus => ({ scripts: [], events: [{ time, script: "heating", kind: "mismatch", message: "AC power" }] });

  it("hides a cleared problem while it stays the same", () => {
    const list = problems([gone], [], auto("2026-11-04T11:00:00Z"), now);
    const cleared = Object.fromEntries(list.map((p) => [p.stamp, now]));
    expect(sortCleared(list, cleared, loaded, now + 1000).shown).toEqual([]);
  });

  it("shows it again when it happens again: a newer event, another detail, or gone and back", () => {
    const first = problems([gone, sensor], [], auto("2026-11-04T11:00:00Z"), now);
    const cleared = Object.fromEntries(first.map((p) => [p.stamp, now]));
    // A newer mismatch and a lower battery are new occurrences.
    const later = problems([gone, device("s", "Door", { dps: [dp("battery_percentage", 8)] })], [], auto("2026-11-04T11:30:00Z"), now);
    expect(sortCleared(later, cleared, loaded, now).shown.map((p) => p.text).sort()).toEqual(["A device isn't doing what heating wants", "Door's battery is low"]);
    // The plug came back online: its clearing is forgotten, so going offline again shows.
    const { keep } = sortCleared(problems([sensor], [], null, now), cleared, loaded, now);
    expect(Object.keys(keep).some((k) => k.startsWith("o-g"))).toBe(false);
    expect(sortCleared(problems([gone], [], null, now), keep, loaded, now).shown.map((p) => p.text)).toEqual(["Plug is offline"]);
  });

  it("forgets nothing while that kind of data is still loading", () => {
    const list = problems([gone], [ext({ name: "Tuya", needsApproval: true })], auto("2026-11-04T11:00:00Z"), now);
    const cleared = Object.fromEntries(list.map((p) => [p.stamp, now]));
    const { keep } = sortCleared(problems([], null, null, now), cleared, { devices: false, extensions: false, automations: false }, now);
    expect(Object.keys(keep).length).toBe(3);
  });

  it("brings a cleared problem back after a day if it's still there", () => {
    const list = problems([gone], [], null, now);
    const cleared = Object.fromEntries(list.map((p) => [p.stamp, now]));
    expect(sortCleared(list, cleared, loaded, now + CLEARED_FOR_MS - 1).shown).toEqual([]);
    expect(sortCleared(list, cleared, loaded, now + CLEARED_FOR_MS).shown.map((p) => p.text)).toEqual(["Plug is offline"]);
  });
});

const BUILTINS = ["builtin/needs-you", "builtin/devices", "builtin/automations", "builtin/heating"];
const noop = () => {};

const heatPanel: RenderedWidget = {
  key: "script:heat/panel", title: "All heaters", from: "heat", practising: true,
  blocks: [{ type: "stat", label: "Average", value: 20.5, unit: "°C" }, { type: "number", name: "temp", label: "Target", value: 21 }, { type: "button", label: "Set all", action: "apply", args: { rooms: "all" } }],
};
const weather: RenderedWidget = { key: "ext:weather/now", title: "Weather now", from: "Weather", blocks: [{ type: "rows", rows: [{ label: "Wind", value: "12 km/h" }] }] };

describe("HomePage", () => {
  it("starts empty, with a way to add widgets", async () => {
    const calls = mockService({ "GET /api/widgets": () => ({ body: { layout: [], widgets: [weather] } }), "PUT /api/dashboard": (b) => ({ body: { layout: (b as { widgets: string[] }).widgets } }) });
    render(<WithRole role="operator"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    expect(await screen.findByText("Make Home yours")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Add widgets" }));
    const picker = within(await screen.findByRole("dialog"));
    expect(picker.getByText(/An automation offers one with/)).toBeTruthy();
    fireEvent.click(picker.getByRole("button", { name: "Add Weather now" }));
    await waitFor(() => expect(calls.find((c) => c.key === "PUT /api/dashboard")?.body).toEqual({ widgets: ["ext:weather/now"] }));
    expect(await picker.findByRole("button", { name: "Weather now is on Home" })).toBeTruthy();
  });

  it("tells a viewer someone else adds widgets", async () => {
    mockService({ "GET /api/widgets": () => ({ body: { layout: [], widgets: [] } }) });
    render(<WithRole role="viewer"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    expect(await screen.findByText(/Someone with the Family role or higher can add widgets/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Add widgets" })).toBeNull();
  });

  it("shows the built-in widgets: what needs you, the devices, the automations and the heating", async () => {
    const automations: AutomationStatus = { scripts: [{ name: "heating", live: true, access: [] }, { name: "lights", live: false, access: [] }], events: [{ time: new Date().toISOString(), script: "heating", kind: "set", message: "set Radiator target to 21" }] };
    mockService({ "GET /api/widgets": () => ({ body: { layout: BUILTINS, widgets: [] } }), "GET /api/extensions": () => ({ body: [ext({ name: "Tuya", needsApproval: true })] }), "GET /api/automations": () => ({ body: automations }) });
    render(<WithRole role="admin"><HomePage devices={[heater, sensor, gone]} rooms={{ Salon: ["h"] }} write={noop} /></WithRole>);
    const needs = within(await screen.findByRole("list", { name: "What needs you" }));
    expect(await needs.findByText("Tuya is waiting for your OK")).toBeTruthy();
    expect(needs.getByText("Plug is offline")).toBeTruthy();
    expect(screen.getByText("of 3 online").parentElement?.textContent).toBe("2 of 3 online");
    expect(await screen.findByText("1 live")).toBeTruthy();
    expect(screen.getByText("1 practising")).toBeTruthy();
    expect(screen.getByRole("list", { name: "What they did lately" }).textContent).toContain("set Radiator target to 21");
    const heating = within(screen.getByRole("list", { name: "Heating now" }));
    expect(heating.getByText("Salon")).toBeTruthy();
    expect(heating.getByText(/Heating to 21/)).toBeTruthy();
  });

  it("clears problems one by one or all at once, and shows one again when it happens again", async () => {
    localStorage.clear();
    mockService({ "GET /api/widgets": () => ({ body: { layout: ["builtin/needs-you"], widgets: [] } }), "GET /api/extensions": () => ({ body: [] }), "GET /api/automations": () => ({ body: { scripts: [], events: [] } }) });
    const { rerender } = render(<WithRole role="admin"><HomePage devices={[sensor, gone, device("x", "Lamp", { online: false })]} rooms={{}} write={noop} /></WithRole>);
    const needs = within(await screen.findByRole("list", { name: "What needs you" }));
    fireEvent.click(needs.getByRole("button", { name: "Clear: Plug is offline" }));
    expect(needs.queryByText("Plug is offline")).toBeNull();
    expect(needs.getByText("Lamp is offline")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear all" }));
    expect(await screen.findByText(/Nothing new needs you/)).toBeTruthy();
    // The plug comes back online, then drops off again: it shows again. The lamp, still offline, stays cleared.
    rerender(<WithRole role="admin"><HomePage devices={[sensor, device("g", "Plug"), device("x", "Lamp", { online: false })]} rooms={{}} write={noop} /></WithRole>);
    await waitFor(() => expect(JSON.parse(localStorage.getItem("needsYouCleared") ?? "{}")).not.toHaveProperty(["o-g|Check it has power and Wi-Fi."]));
    rerender(<WithRole role="admin"><HomePage devices={[sensor, gone, device("x", "Lamp", { online: false })]} rooms={{}} write={noop} /></WithRole>);
    const again = within(await screen.findByRole("list", { name: "What needs you" }));
    expect(again.getByText("Plug is offline")).toBeTruthy();
    expect(again.queryByText("Lamp is offline")).toBeNull();
  });

  it("draws a widget's groups as titled sections, each holding the blocks up to the next group", async () => {
    const grouped: RenderedWidget = {
      key: "script:w/controls", title: "Quick controls", from: "w",
      blocks: [
        { type: "text", text: "Before any group" },
        { type: "group", label: "Szafka" }, { type: "stat", label: "Power", value: 10, unit: "W" },
        { type: "group", label: "Roller blind" }, { type: "rows", rows: [{ label: "Position", value: "70% closed" }] }, { type: "button", label: "Open", action: "open" },
      ],
    };
    mockService({ "GET /api/widgets": () => ({ body: { layout: ["script:w/controls"], widgets: [grouped] } }) });
    render(<WithRole role="operator"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    const szafka = within(await screen.findByRole("region", { name: "Szafka" }));
    expect(szafka.getByRole("heading", { name: "Szafka" })).toBeTruthy();
    expect(szafka.getByText("Power")).toBeTruthy();
    expect(szafka.queryByText("70% closed")).toBeNull();
    const blind = within(screen.getByRole("region", { name: "Roller blind" }));
    expect(blind.getByText("70% closed")).toBeTruthy();
    expect(blind.getByRole("button", { name: "Open" })).toBeTruthy();
    expect(screen.getByText("Before any group").closest("section")).toBeNull();
  });

  it("says all is well when it is", async () => {
    mockService({ "GET /api/widgets": () => ({ body: { layout: ["builtin/needs-you", "builtin/heating"], widgets: [] } }), "GET /api/extensions": () => ({ body: [] }), "GET /api/automations": () => ({ body: { scripts: [], events: [] } }) });
    render(<WithRole role="viewer"><HomePage devices={[device("a", "Lamp")]} rooms={{}} write={noop} /></WithRole>);
    expect(await screen.findByText("All good. Nothing needs you.")).toBeTruthy();
    expect(screen.getByText("Nothing here reports a temperature yet.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Edit Home" })).toBeNull();
  });

  it("presses a script's button with what its inputs hold, and says when the script only practises", async () => {
    const calls = mockService({ "GET /api/widgets": () => ({ body: { layout: ["script:heat/panel"], widgets: [heatPanel] } }), "POST /api/widgets/script%3Aheat/panel/actions/apply": () => ({ body: { live: false } }) });
    render(<WithRole role="operator"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    expect(await screen.findByText("20.5")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Target"), { target: { value: "22.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Set all" }));
    await waitFor(() => expect(calls.find((c) => c.key.startsWith("POST /api/widgets"))?.body).toEqual({ input: { temp: 22.5, rooms: "all" } }));
  });

  it("shows a viewer the widget without its buttons", async () => {
    mockService({ "GET /api/widgets": () => ({ body: { layout: ["script:heat/panel"], widgets: [heatPanel] } }) });
    render(<WithRole role="viewer"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    expect(await screen.findByText("20.5")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Set all" })).toBeNull();
    expect(screen.queryByLabelText("Target")).toBeNull();
  });

  it("keeps a widget whose source is gone, and removes it while editing", async () => {
    const calls = mockService({ "GET /api/widgets": () => ({ body: { layout: ["ext:gone/x", "builtin/devices"], widgets: [] } }), "PUT /api/dashboard": (b) => ({ body: { layout: (b as { widgets: string[] }).widgets } }) });
    render(<WithRole role="operator"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    expect(await screen.findByText(/is gone or stopped/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Edit Home" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove Missing widget" }));
    await waitFor(() => expect(calls.find((c) => c.key === "PUT /api/dashboard")?.body).toEqual({ widgets: ["builtin/devices"] }));
    expect(screen.queryByText(/is gone or stopped/)).toBeNull();
  });

  it("moves a widget with the arrow keys on its handle", async () => {
    const calls = mockService({ "GET /api/widgets": () => ({ body: { layout: ["builtin/devices", "ext:weather/now"], widgets: [weather] } }), "PUT /api/dashboard": (b) => ({ body: { layout: (b as { widgets: string[] }).widgets } }) });
    render(<WithRole role="operator"><HomePage devices={[]} rooms={{}} write={noop} /></WithRole>);
    fireEvent.click(await screen.findByRole("button", { name: "Edit Home" }));
    fireEvent.keyDown(screen.getByRole("button", { name: "Move Weather now" }), { key: "ArrowUp" });
    await waitFor(() => expect(calls.find((c) => c.key === "PUT /api/dashboard")?.body).toEqual({ widgets: ["ext:weather/now", "builtin/devices"] }));
  });
});
