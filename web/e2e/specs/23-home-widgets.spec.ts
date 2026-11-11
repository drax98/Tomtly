import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { apiFromBrowser, open, OWNER, ownerApi, peopleAndDevices, person } from "../support/home";
import { automationsDir, freshHome } from "../support/stack";

let api: APIRequestContext;

// A script's own widget: it counts presses, so a press shows it ran, and logs what it got.
const panel = `
let presses = 0;
home.widget("evening", {
  title: "Evening",
  render: () => [
    { type: "text", text: "Pressed " + presses + " times" },
    { type: "number", name: "temp", label: "Target", value: 20 },
    { type: "button", label: "Set", action: "apply", args: { room: "all" } },
  ],
  actions: { apply: (input) => { presses++; home.log("asked for " + input.temp + " in " + input.room); } },
});
`;

test.beforeEach(async () => {
  await freshHome();
  api = await ownerApi();
  await peopleAndDevices(api);
  writeFileSync(join(automationsDir, "panel.ts"), panel);
  expect((await api.post("/api/automations/reload", { data: {} })).ok()).toBe(true);
});

const widgetTitles = (page: Page) => page.locator('[data-testid^="widget-"]').evaluateAll((cards) => cards.map((c) => c.getAttribute("data-testid")));

test("Home starts empty; an automation's widget goes on it, and its button runs the script", async ({ page }) => {
  await open(page, OWNER, "/home");
  await expect(page.getByText("Make Home yours")).toBeVisible();
  await page.getByRole("button", { name: "Add widgets" }).click();
  const picker = page.getByRole("dialog");
  await expect(picker.getByText("From panel, practising")).toBeVisible();
  await picker.getByRole("button", { name: "Add Evening" }).click();
  await picker.getByRole("button", { name: "Add Devices" }).click();
  await expect(picker.getByRole("button", { name: "Devices is on Home" })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Done" }).click();

  const evening = page.getByTestId("widget-Evening");
  await expect(evening.getByText("Pressed 0 times")).toBeVisible();
  await evening.getByLabel("Target").fill("22.5");
  await evening.getByRole("button", { name: "Set" }).click();
  await expect(page.getByText("panel is practising")).toBeVisible();
  await expect(evening.getByText("Pressed 1 times")).toBeVisible();
  const log = await (await api.get("/api/automations")).json();
  expect(log.events.map((e: { message: string }) => e.message)).toContain("asked for 22.5 in all");

  await page.getByRole("button", { name: "Edit Home" }).click();
  await page.getByRole("button", { name: "Move Devices", exact: true }).press("ArrowUp");
  await expect.poll(() => widgetTitles(page)).toEqual(["widget-Devices", "widget-Evening"]);
  await page.reload();
  await expect.poll(() => widgetTitles(page)).toEqual(["widget-Devices", "widget-Evening"]);
});

test("a viewer sees the widgets without their buttons, and can't rearrange Home", async ({ page }) => {
  expect((await api.put("/api/dashboard", { data: { widgets: ["script:panel/evening"] } })).ok()).toBe(true);
  await open(page, person("viewer"), "/home");
  await expect(page.getByText("Pressed 0 times")).toBeVisible();
  await expect(page.getByRole("button", { name: "Set" })).toBeHidden();
  await expect(page.getByRole("button", { name: "Edit Home" })).toBeHidden();
  expect(await apiFromBrowser(page, "POST", "/api/widgets/script:panel/evening/actions/apply", {})).toBe(403);
});
