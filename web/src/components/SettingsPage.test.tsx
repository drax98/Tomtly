import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mockService } from "@/test/helpers";
import { SettingsPage } from "./SettingsPage";

afterEach(cleanup);
beforeEach(() => vi.unstubAllGlobals());

describe("SettingsPage", () => {
  it("shows what is saved and saves the whole set, with no location as null", async () => {
    const calls = mockService({
      "GET /api/settings": () => ({ body: { language: "pl", timezone: "Europe/Warsaw", latitude: 52.2, longitude: 21 } }),
      "PUT /api/settings": (body) => ({ body }),
    });
    render(<SettingsPage />);
    expect(((await screen.findByLabelText("Timezone")) as HTMLInputElement).value).toBe("Europe/Warsaw");
    expect((screen.getByLabelText("Latitude") as HTMLInputElement).value).toBe("52.2");
    fireEvent.change(screen.getByLabelText("Latitude"), { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Longitude"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((c) => c.key === "PUT /api/settings")).toBe(true));
    expect(calls.find((c) => c.key === "PUT /api/settings")?.body).toEqual({ language: "pl", timezone: "Europe/Warsaw", latitude: null, longitude: null });
  });

  it("shows why the home refused them", async () => {
    mockService({
      "GET /api/settings": () => ({ body: { language: "", timezone: "", latitude: null, longitude: null } }),
      "PUT /api/settings": () => ({ status: 400, body: { error: "there is no timezone called Mars" } }),
    });
    render(<SettingsPage />);
    fireEvent.change(await screen.findByLabelText("Timezone"), { target: { value: "Mars" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("there is no timezone called Mars")).toBeTruthy();
  });
});
