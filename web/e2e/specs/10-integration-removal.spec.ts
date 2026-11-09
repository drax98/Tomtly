import { expect, test } from "@playwright/test";
import { card, confirmDialog, FAKE_HOME, nav, open, OWNER, ownerApi, peopleAndDevices, pressConfirm } from "../support/home";
import { freshHome, restartService } from "../support/stack";

test.beforeEach(async () => {
  await freshHome();
  await peopleAndDevices(await ownerApi());
});

test("removing an integration from config.json holds across a restart, and adding it back needs approving again", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  await card(page, "Fake Home").getByRole("button", { name: "Remove" }).click();
  await expect(confirmDialog(page)).toContainText("Its settings stay in config.json");
  await expect(confirmDialog(page)).toContainText("You can add it back from this page.");
  await pressConfirm(page, "Remove");

  await expect(page.getByRole("heading", { name: "Removed" })).toBeVisible();
  const removed = page.getByRole("listitem").filter({ hasText: "Fake Home" });
  await expect(removed.getByRole("button", { name: "Add back" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No integrations yet" })).toBeVisible();

  await nav(page).getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("heading", { name: "No devices yet" })).toBeVisible();

  await restartService();
  await page.goto("/integrations");
  await expect(page.getByRole("heading", { name: "Removed" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "No integrations yet" })).toBeVisible();
  await page.goto("/devices");
  await expect(page.getByRole("heading", { name: "No devices yet" })).toBeVisible();

  await page.goto("/integrations");
  await page.getByRole("listitem").filter({ hasText: "Fake Home" }).getByRole("button", { name: "Add back" }).click();
  await expect(page.getByText("Added back. If it needs your approval, its card asks.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Removed" })).toBeHidden();

  const fake = card(page, "Fake Home");
  await expect(fake.getByText("Needs your OK")).toBeVisible();
  await expect(fake.getByText("It isn't running.")).toBeVisible();
  await fake.getByRole("button", { name: "Approve access" }).click();
  await expect(confirmDialog(page)).toContainText("Can reach 10.77.0.20:9000");
  await pressConfirm(page, "Approve and start");
  await expect(page.getByText("Approved. It's starting.")).toBeVisible();
  await expect(fake.getByText("Running", { exact: true })).toBeVisible();
  await expect(fake.getByText("5 of 6")).toBeVisible();

  await nav(page).getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
});

test("waiting for approval: nothing runs, and cancelling the dialog changes nothing", async ({ page }) => {
  await freshHome();
  await ownerApi();
  await open(page, OWNER, "/integrations");
  const fake = card(page, "Fake Home");
  await expect(fake.getByText("Needs your OK")).toBeVisible();
  await expect(fake.getByText("Waiting for your OK")).toBeVisible();
  await expect(fake.getByText("No devices from this yet")).toBeVisible();
  await expect(fake.getByRole("button", { name: "Open" })).toBeEnabled();
  await fake.getByRole("button", { name: "Approve access" }).click();
  await confirmDialog(page).getByRole("button", { name: "Cancel" }).click();
  await expect(fake.getByText("Needs your OK")).toBeVisible();
  expect(await (await page.request.get("/api/devices")).json()).toEqual([]);
});

test("the source of a removed integration stays out of the list after reloading the page", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  await card(page, "Fake Home").getByRole("button", { name: "Remove" }).click();
  await pressConfirm(page, "Remove");
  await page.reload();
  await expect(page.getByRole("heading", { name: "Removed" })).toBeVisible();
  await expect(page.getByText(FAKE_HOME)).toHaveCount(0);
});
