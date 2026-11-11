import { expect, test, type APIRequestContext } from "@playwright/test";
import { addFolder, allowAndAdd, approve, card, confirmDialog, open, OWNER, ownerApi, pressConfirm } from "../support/home";
import { freshHome, restartService } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "plugins" });
  api = await ownerApi();
});

test("a required setting has to be filled before a plugin can be added", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await addFolder(page, "plugin", "./extensions/needs-setting");
  await expect(dialog.getByLabel("City name")).toHaveAttribute("required", "");
  await dialog.getByRole("button", { name: "Allow this access and add Needs Setting" }).click();
  await expect(dialog).toBeVisible(); // the browser keeps it open until the field is filled
  await dialog.getByLabel("City name").fill("Gdansk");
  await allowAndAdd(page, dialog, "Needs Setting");
  await expect(card(page, "Needs Setting").getByText(/^Configured: City name.*Units/)).toBeVisible(); // Units has a default
});

test("what a plugin was set up with shows on its card", async ({ page }) => {
  await approve(api, "./extensions/fake-data");
  await open(page, OWNER, "/plugins");
  const data = card(page, "Fake Data");
  await expect(data.getByText("Configured: Station")).toBeVisible();
  await expect(data.getByText("Note")).toHaveCount(0); // an empty optional setting isn't worth a line
  await expect(data.getByText("Saved data")).toBeVisible();
});

test("a plugin asking for nothing says so before it is added, then just runs", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await addFolder(page, "plugin", "./extensions/plain-plugin");
  await expect(dialog.getByText("No access to anything outside itself")).toBeVisible();
  await allowAndAdd(page, dialog, "Plain Plugin");
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

test("a removed plugin stays removed across a restart, and adding it again asks again", async ({ page }) => {
  await approve(api, "./extensions/plain-plugin");
  await open(page, OWNER, "/plugins");
  await card(page, "Plain Plugin").getByRole("button", { name: "Remove" }).click();
  await expect(confirmDialog(page)).toContainText("its saved settings (such as keys) are deleted");
  await pressConfirm(page, "Remove");
  await expect(page.getByRole("heading", { name: "No plugins yet" })).toBeVisible();

  await restartService();
  await page.reload();
  await expect(page.getByRole("heading", { name: "No plugins yet" })).toBeVisible();
  await allowAndAdd(page, await addFolder(page, "plugin", "./extensions/plain-plugin"), "Plain Plugin");
});

test("with none set up the page says how to add one", async ({ page }) => {
  await freshHome({ config: "empty" });
  await ownerApi();
  await open(page, OWNER, "/plugins");
  await expect(page.getByRole("heading", { name: "No plugins yet" })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Add plugin/ })).toBeVisible();
});

test("one that can't even load is refused when added, saying why", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await addFolder(page, "plugin", "./extensions/broken-code");
  await expect(dialog.getByText("That didn't work. Check the details and try again.")).toBeVisible();
  await expect(dialog.getByText(/broken while loading/)).toBeVisible();
  await expect(dialog.getByRole("button", { name: /Allow this access/ })).toHaveCount(0);
});
