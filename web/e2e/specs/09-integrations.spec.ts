import { expect, test, type APIRequestContext } from "@playwright/test";
import { apiFromBrowser, card, confirmDialog, FAKE_HOME, nav, open, OWNER, ownerApi, peopleAndDevices, person, pressConfirm } from "../support/home";
import { freshHome, restartService } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome();
  api = await ownerApi();
  await peopleAndDevices(api);
});

test("an integration's card says it's healthy, what it can reach and how it's set up", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  const fake = card(page, "Fake Home");
  await expect(fake.getByText("Running", { exact: true })).toBeVisible();
  await expect(fake.getByText("Fake devices for the end-to-end tests.")).toBeVisible();
  await expect(fake.getByText("5 of 6")).toBeVisible();
  await expect(fake.getByText("What it can reach")).toBeVisible();
  await expect(fake.getByText("Can reach 10.77.0.20:9000 (the setting host), which may be on your local network")).toBeVisible();
  await expect(fake.getByText("Any device on your local network")).toBeVisible();
  await expect(fake.getByText("Raw TCP and UDP connections")).toBeVisible();
  await expect(fake.getByText("Configured: Device address")).toBeVisible();
  await expect(fake.getByText(FAKE_HOME)).toBeVisible();
  // Set in config.json, so it's changed there and not here.
  await expect(fake.getByRole("button", { name: "Edit settings" })).toHaveCount(0);
  await expect(fake.getByRole("button", { name: "Upload new version" })).toHaveCount(0);
});

test("opening one shows its devices and what the scan found", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Fake Home" })).toBeVisible();
  await expect(page.getByText("6 in the system.")).toBeVisible();
  await expect(page.getByText("Desk lamp")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Cellar sensor" }).getByText("Offline")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Desk lamp" }).getByText("Online")).toBeVisible();

  // The real scan: a TCP sweep and a question to the fake device on the second network.
  await expect(page.getByText("6 of the devices above answered.")).toBeVisible();
  const bulb = page.getByRole("listitem").filter({ hasText: "New bulb" });
  await expect(bulb).toContainText("Fake bulb");
  await expect(bulb).toContainText("10.77.0.21");
  await expect(bulb).toContainText("Press its pairing button to use it.");
  await expect(bulb.getByRole("button", { name: "Add back" })).toHaveCount(0);

  await page.getByRole("button", { name: "Scan again" }).click();
  await expect(page.getByRole("button", { name: "Scan again" })).toBeEnabled();
  await page.getByRole("button", { name: "All integrations" }).click();
  await expect(page).toHaveURL(/\/integrations$/);
});

test("removing a device: gone from pages and commands, survives a restart, and can be added back", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
  const lamp = (await (await api.get("/api/devices")).json()).find((d: { id: string }) => d.id === "fake-home:lamp");
  const dp = lamp.dps.find((d: { code: string }) => d.code === "switch").dp;

  await page.goto("/integrations/fake-home");
  await page.getByRole("button", { name: "Remove Desk lamp" }).click();
  await expect(confirmDialog(page)).toContainText("It leaves the Devices page and takes no more commands");
  await confirmDialog(page).getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("button", { name: "Remove Desk lamp" })).toBeVisible();

  await page.getByRole("button", { name: "Remove Desk lamp" }).click();
  await pressConfirm(page, "Remove");
  await expect(page.getByRole("button", { name: "Remove Desk lamp" })).toHaveCount(0);
  await expect(page.getByText("5 in the system.")).toBeVisible();
  const found = page.getByRole("listitem").filter({ hasText: "Desk lamp" });
  await expect(found.getByRole("button", { name: "Add back" })).toBeVisible();

  // It refuses commands, for the browser's own session too.
  expect(await apiFromBrowser(page, "POST", `/api/devices/fake-home:lamp/dps/${dp}`, { value: true })).toBe(404);

  await nav(page).getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("switch", { name: "Hall thermostat Power" })).toBeVisible();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toHaveCount(0);

  await restartService();
  await page.reload();
  await expect(page.getByRole("switch", { name: "Hall thermostat Power" })).toBeVisible();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toHaveCount(0);

  await page.goto("/integrations/fake-home");
  const back = page.getByRole("listitem").filter({ hasText: "Desk lamp" }).getByRole("button", { name: "Add back" });
  await back.click();
  await expect(page.getByRole("button", { name: "Remove Desk lamp" })).toBeVisible();
  await expect(page.getByText("6 in the system.")).toBeVisible();

  await nav(page).getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
});

test("someone without the permission sees the list but no removal", async ({ page }) => {
  await open(page, person("viewer"), "/integrations/fake-home");
  await expect(page.getByText("Desk lamp")).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove Desk lamp" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Found on your network" })).toHaveCount(0);
});

test("an integration that can't look on the network says so", async ({ page }) => {
  // gh-lamp has no devices.discover.
  await open(page, OWNER, "/integrations");
  await page.getByRole("button", { name: /^Add integration/ }).click();
  await page.getByRole("menuitem", { name: /From a GitHub repo/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Repository").fill("e2e/gh-lamp");
  await dialog.getByLabel("Version").fill("v1.0.0");
  await dialog.getByRole("button", { name: "Check" }).click();
  await dialog.getByRole("button", { name: /Allow this access and add/ }).click();
  await expect(dialog).toBeHidden();
  await card(page, "GitHub Lamp").getByRole("button", { name: "Open" }).click();
  await expect(page.getByText("This integration can't look on the network, so this only lists what you removed.")).toBeVisible();
  await expect(page.getByText("Nothing else found, and nothing removed.")).toBeVisible();
});
