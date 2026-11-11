import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DataPoint, Device } from "@/api";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Pinned, WriteAccess } from "./controls";
import { cardFor, ClimateCard, CoverCard, GenericCard, SensorCard, VacuumCard } from "./cards";

afterEach(cleanup);

const dp = (dpId: string, code: string, over: Partial<DataPoint> = {}): DataPoint => ({
  dp: dpId, code, type: "Boolean", values: {}, scale: 0, writable: false, value: null, ...over,
});
const device = (kind: string, dps: DataPoint[]): Device => ({
  id: "d1", adapter: "t", name: "Thing", kind, category: "", product: "", online: true, dps,
});

const vacuum = device("vacuum", [
  dp("0.0", "command", { type: "Action", writable: true, values: { range: ["start", "pause", "dock", "find"] } }),
  dp("0.1", "fan_speed", { type: "Enum", writable: true, values: { range: ["silent", "strong"] }, value: "strong" }),
  dp("0.2", "state", { type: "Enum", value: "charging" }),
  dp("0.3", "battery", { type: "Integer", value: 80, values: { unit: "%" } }),
  dp("0.4", "error_code", { type: "Integer", value: 0 }),
]);

describe("VacuumCard", () => {
  it("shows state and battery, and sends each action", () => {
    const write = vi.fn();
    render(<VacuumCard device={vacuum} write={write} />);
    expect(screen.getByText("Charging")).toBeTruthy();
    expect(screen.getByText("80%")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Go to dock" }));
    expect(write).toHaveBeenCalledWith("d1", "0.0", "dock", false);
  });

  it("shows an error code instead of the state", () => {
    const failed = { ...vacuum, dps: vacuum.dps!.map((d) => (d.code === "error_code" ? { ...d, value: 5 } : d)) };
    render(<VacuumCard device={failed} write={vi.fn()} />);
    expect(screen.getByText("Error 5")).toBeTruthy();
  });
});

describe("ClimateCard", () => {
  it("says why the target can't change while it's off", () => {
    const device = {
      id: "t", adapter: "x", name: "Salon", kind: "climate", category: "", product: "", online: true,
      dps: [
        { dp: "1", code: "switch", type: "Boolean", values: {}, scale: 0, writable: true, value: false },
        { dp: "2", code: "temp_set", type: "Integer", values: { min: 50, max: 350, step: 5 }, scale: 1, writable: true, value: 210 },
      ],
    };
    render(<ClimateCard device={device} write={vi.fn()} />);
    expect((screen.getByRole("button", { name: "Raise Salon" }) as HTMLButtonElement).disabled).toBe(true);
    screen.getByText("Turn it on to change the target.");
  });

  const thermostat = device("climate", [
    dp("125", "switch", { writable: true, value: true }),
    dp("126", "temp_set", { type: "Integer", writable: true, scale: 1, value: 200, values: { min: 195, max: 205, step: 5, unit: "°C" } }),
    dp("127", "temp_current", { type: "Integer", scale: 1, value: 205, values: { unit: "°C" } }),
    dp("102", "heating_state", { value: true }),
  ]);

  it("shows the temperature and that it is heating", () => {
    render(<ClimateCard device={thermostat} write={vi.fn()} />);
    expect(screen.getByText("20.5 °C")).toBeTruthy();
    expect(screen.getByText(/^Heating to/)).toBeTruthy();
  });

  it("steps the target in device units and stops at the limits", () => {
    const write = vi.fn();
    render(<ClimateCard device={thermostat} write={write} />);
    fireEvent.click(screen.getByRole("button", { name: "Raise Thing" }));
    expect(write).toHaveBeenCalledWith("d1", "126", 205);
    write.mockClear();
    const atMax = { ...thermostat, dps: thermostat.dps!.map((d) => (d.code === "temp_set" ? { ...d, value: 205 } : d)) };
    cleanup();
    render(<ClimateCard device={atMax} write={write} />);
    fireEvent.click(screen.getByRole("button", { name: "Raise Thing" }));
    expect(write).not.toHaveBeenCalled();
  });
});

describe("values without a place on a dedicated card", () => {
  it("stay off the card, which leaves them to its drawer, unless they're pinned", () => {
    const ac = device("climate", [
      dp("temp_current", "temp_current", { type: "Integer", value: 23, values: { unit: "°C" } }),
      dp("swing", "swing_vertical", { type: "Enum", writable: true, value: "full", values: { range: ["default", "full"] } }),
    ]);
    const { unmount } = render(<ClimateCard device={ac} write={vi.fn()} />);
    expect(screen.queryByText(/Airflow up and down/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Advanced settings" })).toBeNull();
    unmount();
    render(<Pinned.Provider value={["swing"]}><ClimateCard device={ac} write={vi.fn()} /></Pinned.Provider>);
    expect(screen.getByTestId("pinned").textContent).toContain("Airflow up and down");
  });

  it("send an action from a pinned value on a generic card", () => {
    const write = vi.fn();
    const purifier = device("air", [dp("command", "command", { type: "Action", writable: true, values: { range: ["next_mode"] } })]);
    render(<Pinned.Provider value={["command"]}><GenericCard device={purifier} write={write} /></Pinned.Provider>);
    fireEvent.click(screen.getByRole("button", { name: "Next mode" }));
    expect(write).toHaveBeenCalledWith("d1", "command", "next_mode", false);
  });
});

describe("devices with codes a dedicated card doesn't use", () => {
  it("gives a Tuya vacuum and a garage opener the generic card", () => {
    expect(cardFor(device("vacuum", [dp("power_go", "power_go", { writable: true })]))).toBe(GenericCard);
    expect(cardFor(device("cover", [dp("switch_1", "switch_1", { writable: true })]))).toBe(GenericCard);
  });

  it("shows no position for a cover that hasn't reported one", () => {
    render(<CoverCard device={device("cover", [dp("control", "control", { type: "Enum", writable: true, value: null, values: { range: ["on", "off", "stop"] } })])} write={vi.fn()} />);
    expect(screen.queryByText("Closed")).toBeNull();
  });

  it("sends a cover's own word for open", () => {
    const write = vi.fn();
    render(<CoverCard device={device("cover", [dp("control", "control", { type: "Enum", writable: true, value: null, values: { range: ["on", "off", "stop"] } })])} write={write} />);
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(write).toHaveBeenCalledWith("d1", "control", "on", false);
  });

  it("gives each channel of a multi-gang switch its own row", () => {
    render(<GenericCard device={device("switch", [dp("switch_1", "switch_1", { writable: true }), dp("switch_2", "switch_2", { writable: true })])} write={vi.fn()} />);
    expect(screen.getByText("Switch 1")).toBeTruthy();
    expect(screen.getByText("Switch 2")).toBeTruthy();
  });
});

describe("SensorCard", () => {
  it.each([[true, "Open"], [false, "Closed"], [null, "Unknown"]])("reads %s as %s", (value, text) => {
    render(<SensorCard device={device("sensor", [dp("101", "open", { value })])} write={vi.fn()} />);
    expect(screen.getByText(text)).toBeTruthy();
  });
});

describe("cards for adapters we have never heard of", () => {
  const custom = device("aquarium", [
    dp("a", "power", { writable: true, value: false }),
    dp("b", "water_temp", { type: "Integer", value: 24, values: { unit: "°C" } }),
    dp("c", "feed", { type: "Action", writable: true, values: { range: ["now"] } }),
  ]);

  it("falls back to a card that still works", () => {
    const write = vi.fn();
    const Card = cardFor(custom);
    expect(Card).toBe(GenericCard);
    render(<Card device={custom} write={write} />);
    expect(screen.getByText("24 °C")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Now" }));
    expect(write).toHaveBeenCalledWith("d1", "c", "now", false);
    fireEvent.click(screen.getByRole("switch"));
    expect(write).toHaveBeenCalledWith("d1", "a", true);
  });

  it("doesn't crash on a device with no data points", () => {
    const empty = { ...custom, dps: null };
    const Card = cardFor(empty);
    render(<Card device={empty} write={vi.fn()} />);
    expect(screen.getByText("Thing")).toBeTruthy();
  });
});

describe("read-only viewers", () => {
  it("disables every control and says why, without hiding the device", () => {
    const write = vi.fn();
    render(
      <TooltipProvider>
        <WriteAccess.Provider value={false}>
          <VacuumCard device={vacuum} write={write} />
          <GenericCard device={device("light", [dp("a", "switch_led", { writable: true, value: false })])} write={write} />
        </WriteAccess.Provider>
      </TooltipProvider>,
    );
    expect((screen.getByRole("button", { name: "Go to dock" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("switch") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Go to dock" }));
    expect(write).not.toHaveBeenCalled();
    expect(screen.getAllByText("Thing").length).toBe(2);
  });
});

describe("devices described by their spec", () => {
  const many = Array.from({ length: 12 }, (_, i) => dp(`p${i}`, `reading_${i}`, { type: "Integer", value: i }));
  const setting = dp("s", "target_time", { type: "Integer", writable: true, value: 3, values: { min: 0, max: 9 } });

  it("keeps a long list short, with what can be changed first, and leaves the rest to the drawer", () => {
    render(<GenericCard device={device("appliance", [...many, setting])} write={vi.fn()} />);
    expect(screen.getByText("Target time")).toBeTruthy();
    expect(screen.queryByText("Reading 11")).toBeNull();
    expect(screen.getByText("Reading 6")).toBeTruthy();
  });

  it("leaves a short list as it was", () => {
    render(<GenericCard device={device("appliance", many.slice(0, 8))} write={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Advanced settings" })).toBeNull();
  });

  it("says why a device can't be read, instead of blaming its Wi-Fi", () => {
    const note = "Bluetooth or Mesh, so it has no local network connection.";
    render(<GenericCard device={{ ...device("sensor", [dp("u", "unavailable", { type: "String", value: note })]), online: false }} write={vi.fn()} />);
    expect(screen.getByText(note)).toBeTruthy();
    expect(screen.queryByText(/power and Wi-Fi/)).toBeNull();
    expect(screen.queryByText("Unavailable")).toBeNull();
  });
});
