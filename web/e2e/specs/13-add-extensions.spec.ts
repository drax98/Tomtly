import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { join } from "node:path";
import { card, confirmDialog, open, OWNER, ownerApi, pressConfirm } from "../support/home";
import { freshHome, restartService, uploads } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "empty" });
  api = await ownerApi();
});

async function localFiles(page: Page, what: "plugin" | "integration") {
  await page.getByRole("button", { name: new RegExp(`^Add ${what}`) }).click();
  await page.getByRole("menuitem", { name: /From local files/ }).click();
  return page.getByRole("dialog", { name: `Add a${what === "integration" ? "n" : ""} ${what} from local files` });
}

async function addTide(page: Page) {
  const dialog = await localFiles(page, "plugin");
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "tide-plugin.ts"));
  await dialog.getByLabel("Station").fill("Hel");
  await dialog.getByLabel("Tide key").fill("tide-secret-value");
  await dialog.getByRole("button", { name: "Allow this access and add Tide Plugin" }).click();
  await expect(dialog).toBeHidden();
  await expect(card(page, "Tide Plugin").getByText("Running", { exact: true })).toBeVisible();
}

test("the Add button offers all three places, wherever you start", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  await page.getByRole("button", { name: /^Add plugin/ }).click();
  await expect(page.getByRole("menuitem", { name: /From the marketplace/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: /From a GitHub repo/ })).toBeVisible();
  await page.getByRole("menuitem", { name: /From the marketplace/ }).click();
  await expect(page).toHaveURL(/\/marketplace$/);

  await page.goto("/integrations");
  await page.getByRole("button", { name: /^Add integration/ }).click();
  await expect(page.getByRole("menuitem", { name: /From local files/ })).toBeVisible();
});

test("uploading a file: you see what it asks for before anything runs", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await localFiles(page, "plugin");
  await expect(dialog.getByText("Nothing runs yet: you'll see what it wants first.")).toBeVisible();
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "tide-plugin.ts"));

  await expect(dialog.getByText("Uploaded file: tide-plugin.ts")).toBeVisible();
  await expect(dialog.getByText("It needs this access:")).toBeVisible();
  await expect(dialog.getByText("Saved data")).toBeVisible();
  await expect(dialog.getByText("Which tide station to read.")).toBeVisible();

  await dialog.getByRole("button", { name: "Back" }).click();
  await expect(dialog.getByRole("button", { name: "Choose file" })).toBeVisible();
  expect(await (await api.get("/api/extensions")).json()).toEqual([]);

  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "tide-plugin.ts"));
  await dialog.getByLabel("Station").fill("Hel");
  await dialog.getByLabel("Tide key").fill("tide-secret-value");
  await dialog.getByRole("switch", { name: "Enabled" }).click();
  await dialog.getByRole("button", { name: "Allow this access and add Tide Plugin" }).click();

  await expect(dialog).toBeHidden();
  await expect(page.getByText("Tide Plugin added")).toBeVisible();
  const tide = card(page, "Tide Plugin");
  await expect(tide.getByText("Running", { exact: true })).toBeVisible();
  await expect(tide.getByText("Uploaded file")).toBeVisible();
  await expect(tide.getByText("Configured: Tide key, Enabled, Station")).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText("tide-secret-value");
  await tide.getByRole("button", { name: "Details for scripts" }).click();
  await expect(tide.getByText('"station": "Hel"')).toBeVisible();

  // Added on the page, so it survives a restart with its settings, and no approval.
  await restartService();
  await page.reload();
  await expect(card(page, "Tide Plugin").getByText("Running", { exact: true })).toBeVisible();
});

test("a required field stops the form, and the service checks too", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await localFiles(page, "plugin");
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "tide-plugin.ts"));
  await dialog.getByRole("button", { name: "Allow this access and add Tide Plugin" }).click();
  await expect(dialog.getByLabel("Station")).toHaveJSProperty("validity.valueMissing", true);
  await expect(dialog).toBeVisible();
  expect(await (await api.get("/api/extensions")).json()).toEqual([]);
});

test("a file that isn't an extension is refused with the reason", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await localFiles(page, "plugin");
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "not-an-archive.zip"));
  await expect(dialog.getByText("That didn't work. Check the details and try again.")).toBeVisible();
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "broken.ts"));
  await expect(dialog.getByText("That didn't work. Check the details and try again.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: /Allow this access/ })).toHaveCount(0);
});

test("a folder in the config folder can be added; one outside it can't", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await localFiles(page, "plugin");
  const check = dialog.getByRole("button", { name: "Check" });
  await expect(check).toBeDisabled();
  await dialog.getByLabel("Or use a folder in your config folder").fill("../../etc");
  await check.click();
  await expect(dialog.getByText("That didn't work. Check the details and try again.")).toBeVisible();

  await dialog.getByLabel("Or use a folder in your config folder").fill("./extensions/folder-plugin");
  await check.click();
  await dialog.getByRole("button", { name: "Allow this access and add Folder Plugin" }).click();
  await expect(dialog).toBeHidden();
  await expect(card(page, "Folder Plugin").getByText("Running", { exact: true })).toBeVisible();
});

test("the GitHub dialog explains what's wrong", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  await page.getByRole("button", { name: /^Add integration/ }).click();
  await page.getByRole("menuitem", { name: /From a GitHub repo/ }).click();
  const dialog = page.getByRole("dialog", { name: "Add an integration from a GitHub repo" });
  const check = dialog.getByRole("button", { name: "Check" });
  await expect(check).toBeDisabled();
  await dialog.getByLabel("Repository").fill("e2e/no-such-repo");
  await expect(check).toBeDisabled();
  await dialog.getByLabel("Version").fill("v1.0.0");
  await check.click();
  await expect(dialog.getByText("github answered 404")).toBeVisible();

  await dialog.getByLabel("Repository").fill("not a repo");
  await check.click();
  await expect(dialog.getByText(/must look like github:owner\/repo@ref/)).toBeVisible();

  await dialog.getByLabel("Repository").fill("e2e/gh-lamp");
  await dialog.getByLabel("Version").fill("v9.9.9");
  await check.click();
  await expect(dialog.getByText(/github answered 404.*gh-lamp@v9\.9\.9/)).toBeVisible();
  await expect(dialog.getByRole("button", { name: /Allow this access/ })).toHaveCount(0);
});

test("adding from GitHub by a pasted address, then updating from GitHub", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  await page.getByRole("button", { name: /^Add integration/ }).click();
  await page.getByRole("menuitem", { name: /From a GitHub repo/ }).click();
  const dialog = page.getByRole("dialog", { name: "Add an integration from a GitHub repo" });
  await dialog.getByLabel("Repository").fill("https://github.com/e2e/gh-lamp.git");
  await dialog.getByLabel("Version").fill("v1.0.0");
  await dialog.getByRole("button", { name: "Check" }).click();
  await expect(dialog.getByText("No access to anything outside itself")).toBeVisible();
  await dialog.getByRole("button", { name: "Allow this access and add GitHub Lamp" }).click();
  await expect(dialog).toBeHidden();

  const lamp = card(page, "GitHub Lamp");
  await expect(lamp.getByText("Running", { exact: true })).toBeVisible();
  await expect(lamp.getByText("Version 1.0.0")).toBeVisible();
  await lamp.getByRole("button", { name: "Open" }).click();
  await expect(page.getByText("github:e2e/gh-lamp@v1.0.0", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Update from GitHub" }).click();
  const update = page.getByRole("dialog", { name: "Update from GitHub" });
  const check = update.getByRole("button", { name: "Check" });
  await expect(check).toBeDisabled();
  await update.getByLabel("New version").fill("github:e2e/gh-lamp@v1.1.0");
  await check.click();
  const replace = page.getByRole("dialog", { name: "Replace GitHub Lamp 1.0.0 with version 1.1.0?" });
  await expect(replace.getByText("It asks for the same access as before.")).toBeVisible();
  await replace.getByRole("button", { name: "Replace" }).click();
  await expect(page.getByText("Replaced. It's restarting.")).toBeVisible();
  await expect(page.getByText("github:e2e/gh-lamp@v1.1.0", { exact: true })).toBeVisible();
  await expect(page.getByRole("definition").filter({ hasText: /^1\.1\.0$/ })).toBeVisible();
});

test("editing settings: a value can change, a secret stays unless typed, and it restarts", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  await addTide(page);
  const tide = card(page, "Tide Plugin");
  await tide.getByRole("button", { name: "Edit settings" }).click();
  const dialog = page.getByRole("dialog", { name: "Settings for Tide Plugin" });
  await expect(dialog.getByLabel("Station")).toHaveValue("Hel");
  await expect(dialog.getByLabel("Tide key")).toHaveValue("");
  await expect(dialog.getByLabel("Tide key")).toHaveAttribute("placeholder", "Leave blank to keep it");
  await dialog.getByLabel("Station").fill("Sopot");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Saved. It's restarting.")).toBeVisible();
  await expect(tide.getByText("Configured: Tide key, Station")).toBeVisible();
  await tide.getByRole("button", { name: "Details for scripts" }).click();
  await expect(tide.getByText('"station": "Sopot"')).toBeVisible();

  await tide.getByRole("button", { name: "Edit settings" }).click();
  await dialog.getByLabel("Station").fill("");
  await dialog.getByLabel("Station").press("Enter");
  await expect(dialog).toBeVisible(); // the browser's required check
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
});

test("uploading a new version: same access just restarts, more access waits, another id is refused", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  await addTide(page);
  const tide = card(page, "Tide Plugin");
  const picker = tide.getByLabel("File with the new version");

  await picker.setInputFiles(join(uploads, "tide-plugin-other-id.ts"));
  const refused = page.getByRole("dialog", { name: /^Replace Tide Plugin/ });
  await refused.getByRole("button", { name: "Replace" }).click();
  await expect(refused.getByRole("alert")).toBeVisible();
  await refused.getByRole("button", { name: "Cancel" }).click();
  await expect(tide.getByRole("definition").filter({ hasText: /^1\.0\.0$/ })).toBeVisible();

  await picker.setInputFiles(join(uploads, "tide-plugin-v2.ts"));
  const same = page.getByRole("dialog", { name: "Replace Tide Plugin 1.0.0 with version 1.1.0?" });
  await expect(same.getByText("It asks for the same access as before.")).toBeVisible();
  await same.getByRole("button", { name: "Replace" }).click();
  await expect(page.getByText("Replaced. It's restarting.")).toBeVisible();
  await expect(tide.getByRole("definition").filter({ hasText: /^1\.1\.0$/ })).toBeVisible();
  await expect(tide.getByText("Running", { exact: true })).toBeVisible();
  await tide.getByRole("button", { name: "Details for scripts" }).click();
  await expect(tide.getByText('"version": "1.1.0"')).toBeVisible();

  await picker.setInputFiles(join(uploads, "tide-plugin-v3.ts"));
  const more = page.getByRole("dialog", { name: "Replace Tide Plugin 1.1.0 with version 2.0.0?" });
  await expect(more.getByText("This version asks for access the old one didn't have")).toBeVisible();
  await expect(more.getByRole("listitem").filter({ hasText: /^Any device on your local network$/ })).toBeVisible();
  await expect(more.getByText("The risk in approving this")).toBeVisible();
  await more.getByRole("button", { name: "Replace" }).click();
  await expect(page.getByText("Replaced. It's waiting for your OK.")).toBeVisible();
  await expect(tide.getByText("Needs your OK")).toBeVisible();
  await tide.getByRole("button", { name: "Approve access" }).click();
  await pressConfirm(page, "Approve and start");
  await expect(tide.getByText("Running", { exact: true })).toBeVisible();
  await expect(tide.getByRole("definition").filter({ hasText: /^2\.0\.0$/ })).toBeVisible();
});

test("removing one added on the page: its settings go too, and it isn't offered back", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  await addTide(page);
  await card(page, "Tide Plugin").getByRole("button", { name: "Remove" }).click();
  await expect(confirmDialog(page)).toContainText("its saved settings (such as keys) are deleted");
  await pressConfirm(page, "Remove");
  await expect(page.getByRole("heading", { name: "No plugins yet" })).toBeVisible();
});

test("a webhook's address is shown once and a message reaches the plugin", async ({ page, playwright }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await localFiles(page, "plugin");
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "hook-plugin.ts"));
  await expect(dialog.getByText("Receive messages from outside")).toBeVisible();
  await dialog.getByRole("button", { name: "Allow this access and add Hook Plugin" }).click();

  const shown = page.getByRole("dialog", { name: "Copy the webhook address now" });
  await expect(shown.getByText("This is the only time it is shown")).toBeVisible();
  const address = await shown.getByTestId("webhook-address").innerText();
  expect(address).toMatch(/^http:\/\/localhost:8099\/hooks\/hook-plugin\/[A-Za-z0-9_-]+$/);
  await shown.getByRole("button", { name: "I've saved it" }).click();
  await expect(shown).toBeHidden();

  // Sent with no session at all, the way an outside service would.
  const outsider = await playwright.request.newContext();
  const answer = await outsider.post(`${address}/deep/path`, { data: { said: "hello from outside" } });
  expect(answer.status()).toBe(202);
  expect(await answer.json()).toEqual({ received: true });

  const hook = card(page, "Hook Plugin");
  await hook.getByRole("button", { name: "Details for scripts" }).click();
  await expect(hook.getByText('"said": "hello from outside"')).toBeVisible();
  await expect(hook.getByText('"path": "/deep/path"')).toBeVisible();

  await page.reload();
  await expect(page.getByTestId("webhook-address")).toHaveCount(0);
  await expect(page.getByRole("main")).not.toContainText(address.split("/").at(-1)!);

  expect((await outsider.post(address.replace(/.$/, "x"), { data: { said: "wrong secret" } })).status()).toBe(404);
  await outsider.dispose();
});

test("a new webhook address replaces the old one straight away", async ({ page, playwright }) => {
  await open(page, OWNER, "/plugins");
  const dialog = await localFiles(page, "plugin");
  await dialog.getByLabel("File for the plugin").setInputFiles(join(uploads, "hook-plugin.ts"));
  await dialog.getByRole("button", { name: "Allow this access and add Hook Plugin" }).click();
  const shown = page.getByRole("dialog", { name: "Copy the webhook address now" });
  const old = await shown.getByTestId("webhook-address").innerText();
  await shown.getByRole("button", { name: "I've saved it" }).click();

  await card(page, "Hook Plugin").getByRole("button", { name: "New webhook address" }).click();
  await expect(confirmDialog(page)).toContainText("The old address stops working straight away.");
  await pressConfirm(page, "Make a new address");
  const fresh = await shown.getByTestId("webhook-address").innerText();
  expect(fresh).not.toBe(old);
  await shown.getByRole("button", { name: "I've saved it" }).click();

  const outsider = await playwright.request.newContext();
  expect((await outsider.post(old, { data: { said: "old" } })).status()).toBe(404);
  expect((await outsider.post(fresh, { data: { said: "new" } })).status()).toBe(202);
  await outsider.dispose();
});
