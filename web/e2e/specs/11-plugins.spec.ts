import { expect, test, type APIRequestContext } from "@playwright/test";
import { approve, card, confirmDialog, open, OWNER, ownerApi, pressConfirm } from "../support/home";
import { freshHome, restartService } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "plugins" });
  api = await ownerApi();
});

test("what a plugin needs shows on its card, in words", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const needs = card(page, "Needs Setting");
  await expect(needs.getByText("Missing: City name")).toBeVisible();
  await expect(needs.getByText("Problem", { exact: true })).toBeVisible();
  await expect(needs.getByText("This has a problem")).toBeVisible();
  await expect(needs.getByText("Configured: Units")).toBeVisible(); // it has a default

  const data = card(page, "Fake Data");
  await expect(data.getByText("Configured: Station")).toBeVisible();
  await expect(data.getByText("Note")).toHaveCount(0); // an empty optional setting isn't worth a line
  await expect(data.getByText("Saved data")).toBeVisible();
});

test("a plugin asking for nothing just runs; one asking for access waits", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const plain = card(page, "Plain Plugin");
  await expect(plain.getByText("Running", { exact: true })).toBeVisible();
  await expect(plain.getByText("No access to anything outside itself")).toBeVisible();
  await expect(card(page, "Fake Data").getByText("Needs your OK")).toBeVisible();
});

test("Details for scripts shows how to read it, its actions and its data", async ({ page }) => {
  await approve(api, "./extensions/fake-data");
  await open(page, OWNER, "/plugins");
  const data = card(page, "Fake Data");
  await expect(data.getByText("Running", { exact: true })).toBeVisible();
  await expect(data.getByText(/Updated \d/)).toBeVisible();
  await data.getByRole("button", { name: "Details for scripts" }).click();
  await expect(data.getByText('home.plugin("fake-data")')).toBeVisible();
  await expect(data.getByText("Actions: remember")).toBeVisible();
  await expect(data.getByText('"station": "Gdansk"')).toBeVisible();
  await expect(data.getByText('"temperature": 21')).toBeVisible();
});

test("removing a plugin holds across a restart; one asking for nothing comes straight back", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  await card(page, "Plain Plugin").getByRole("button", { name: "Remove" }).click();
  await expect(confirmDialog(page)).toContainText("Automations that use it will stop working.");
  await pressConfirm(page, "Remove");
  await expect(page.getByRole("heading", { name: "Removed" })).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Plain Plugin" })).toBeVisible();

  await restartService();
  await page.reload();
  const removed = page.getByRole("listitem").filter({ hasText: "Plain Plugin" });
  await expect(removed.getByRole("button", { name: "Add back" })).toBeVisible();
  await removed.getByRole("button", { name: "Add back" }).click();
  await expect(card(page, "Plain Plugin").getByText("Running", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Removed" })).toBeHidden();
});

test("a removed plugin that holds data needs approving again and gets its data back", async ({ page }) => {
  await approve(api, "./extensions/fake-data");
  await open(page, OWNER, "/plugins");
  await expect(card(page, "Fake Data").getByText("Running", { exact: true })).toBeVisible();
  await card(page, "Fake Data").getByRole("button", { name: "Remove" }).click();
  await pressConfirm(page, "Remove");
  await page.getByRole("listitem").filter({ hasText: "Fake Data" }).getByRole("button", { name: "Add back" }).click();
  const data = card(page, "Fake Data");
  await expect(data.getByText("Needs your OK")).toBeVisible();
  await data.getByRole("button", { name: "Approve access" }).click();
  await pressConfirm(page, "Approve and start");
  await expect(data.getByText("Running", { exact: true })).toBeVisible();
  await data.getByRole("button", { name: "Details for scripts" }).click();
  await expect(data.getByText('"station": "Gdansk"')).toBeVisible();
});

test("with none set up the page says how to add one", async ({ page }) => {
  await freshHome({ config: "empty" });
  await ownerApi();
  await open(page, OWNER, "/plugins");
  await expect(page.getByRole("heading", { name: "No plugins yet" })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Add plugin/ })).toBeVisible();
});

test("one that can't even load still gets a card saying why", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const broken = card(page, "./extensions/broken-code");
  await expect(broken.getByText("Problem", { exact: true })).toBeVisible();
  await expect(broken.getByText(/broken while loading/)).toBeVisible();
});
