import { act, cleanup, render, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { session, type Device } from "@/api";
import { mockService, type Routes } from "@/test/helpers";
import { usePoll, useDevices } from "./useDevices";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const lamp = (on: boolean): Device => ({
  id: "lamp", adapter: "tuya", name: "Lamp", kind: "light", category: "", product: "", online: true,
  dps: [{ dp: "1", code: "switch_led", type: "Boolean", values: {}, scale: 0, writable: true, value: on }],
});
const power = (devices: Device[] | null) => devices?.[0]?.dps?.[0]?.value;

// Flushes the pending fetches that a poll or a write starts.
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));

function serve(state: { on: boolean }, extra: Routes = {}) {
  return mockService({
    "GET /api/devices": () => ({ body: [lamp(state.on)] }),
    "GET /api/rooms": () => ({ body: { Hall: ["lamp"] } }),
    "POST /api/devices/lamp/dps/1": () => ({ body: {} }),
    ...extra,
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  session.expired = false;
});

describe("usePoll", () => {
  function Probe({ fn }: { fn: () => Promise<void> }) {
    usePoll(fn);
    return null;
  }

  it("asks every two seconds, and stops when the page goes away", async () => {
    const fn = vi.fn(async () => {});
    const { unmount } = render(<Probe fn={fn} />);
    await flush();
    expect(fn).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(1999));
    expect(fn).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(fn).toHaveBeenCalledTimes(2);
    unmount();
    await act(() => vi.advanceTimersByTimeAsync(10000));
    expect(fn).toHaveBeenCalledTimes(2);
  });

  it("stops asking while the session is expired, so it doesn't pile up refused calls", async () => {
    const fn = vi.fn(async () => {});
    render(<Probe fn={fn} />);
    await flush();
    session.expired = true;
    await act(() => vi.advanceTimersByTimeAsync(6000));
    expect(fn).toHaveBeenCalledTimes(1);
    session.expired = false;
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(fn).toHaveBeenCalledTimes(2);
  });
});

describe("useDevices", () => {
  it("loads the devices and the rooms they are in", async () => {
    serve({ on: false });
    const { result } = renderHook(() => useDevices());
    expect(result.current.devices).toBeNull();
    await flush();
    expect(result.current.devices?.map((d) => d.id)).toEqual(["lamp"]);
    expect(result.current.rooms).toEqual({ Hall: ["lamp"] });
    expect(result.current.reachable).toBe(true);
  });

  it("falls back to no rooms, and says the devices are out of reach when the list fails", async () => {
    serve({ on: false }, {
      "GET /api/devices": () => ({ status: 500, body: { error: "down" } }),
      "GET /api/rooms": () => ({ status: 500, body: { error: "down" } }),
    });
    const { result } = renderHook(() => useDevices());
    await flush();
    expect(result.current.rooms).toEqual({});
    expect(result.current.reachable).toBe(false);
    expect(result.current.devices).toBeNull();
  });

  it("shows a write at once, and sends it as the device's value", async () => {
    const calls = serve({ on: false });
    const { result } = renderHook(() => useDevices());
    await flush();
    await act(() => result.current.write("lamp", "1", true));
    expect(power(result.current.devices)).toBe(true);
    expect(calls.find((c) => c.key === "POST /api/devices/lamp/dps/1")?.body).toEqual({ value: true });
  });

  it("keeps a write showing until the device confirms it or twelve seconds pass", async () => {
    serve({ on: false });
    const { result } = renderHook(() => useDevices());
    await flush();
    await act(() => result.current.write("lamp", "1", true));
    await act(() => vi.advanceTimersByTimeAsync(10000));
    expect(power(result.current.devices)).toBe(true);
    await act(() => vi.advanceTimersByTimeAsync(2000));
    expect(power(result.current.devices)).toBe(false);
  });

  it("does not show a write that asked not to be tracked", async () => {
    serve({ on: false });
    const { result } = renderHook(() => useDevices());
    await flush();
    await act(() => result.current.write("lamp", "1", true, false));
    expect(power(result.current.devices)).toBe(false);
  });

  it("drops the write and says why when it is refused", async () => {
    serve({ on: false }, { "POST /api/devices/lamp/dps/1": () => ({ status: 403, body: { error: "Only family can switch lights" } }) });
    const { result } = renderHook(() => useDevices());
    await flush();
    await act(() => result.current.write("lamp", "1", true));
    expect(power(result.current.devices)).toBe(false);
    expect(toast.error).toHaveBeenCalledWith("Only family can switch lights", { description: undefined });
  });

  it("explains a device or network failure with what to check", async () => {
    serve({ on: false }, { "POST /api/devices/lamp/dps/1": () => ({ status: 500, body: { error: "device offline" } }) });
    const { result } = renderHook(() => useDevices());
    await flush();
    await act(() => result.current.write("lamp", "1", true));
    expect(toast.error).toHaveBeenCalledWith("That didn't go through", {
      description: "device offline. Check the device has power and Wi-Fi, then try again.",
    });
  });

  it("falls back to the status code as the reason when the answer has none", async () => {
    serve({ on: false }, { "POST /api/devices/lamp/dps/1": () => ({ status: 500, body: {} }) });
    const { result } = renderHook(() => useDevices());
    await flush();
    await act(() => result.current.write("lamp", "1", true));
    expect(toast.error).toHaveBeenCalledWith("That didn't go through", {
      description: "Request failed with status 500. Check the device has power and Wi-Fi, then try again.",
    });
  });
});
