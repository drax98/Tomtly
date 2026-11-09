import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { join } from "node:path";
import { approve, card, confirmDialog, open, OWNER, ownerApi, pressConfirm } from "../support/home";
import { deviceState, freshHome, uploads } from "../support/stack";

const KEY = "e2e-key-that-must-not-leave";
let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "security" });
  api = await ownerApi();
});

const pluginsPage = (page: Page) => open(page, OWNER, "/plugins");

test("a rogue extension's risk shows on its card and when approving", async ({ page }) => {
  await pluginsPage(page);
  const rogue = card(page, "Rogue Leak");
  await expect(rogue.getByText("Needs your OK")).toBeVisible();
  await expect(rogue.getByText("The risk in approving this")).toBeVisible();
  await expect(rogue.getByText("It holds your API key and can also send data to the address in the setting host.")).toBeVisible();

  await rogue.getByRole("button", { name: "Approve access" }).click();
  const dialog = confirmDialog(page);
  await expect(dialog).toContainText("You're agreeing that it may:");
  await expect(dialog).toContainText("Can reach 10.77.0.20:9000 (the setting host)");
  await expect(dialog.getByText("The risk in approving this")).toBeVisible();
  await expect(dialog).toContainText("A rogue extension could steal it this way");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(rogue.getByText("Needs your OK")).toBeVisible();
  await expect.poll(async () => (await deviceState()).requests.length).toBe(0);
});

test("a key sent as written is refused and shown, and nothing reaches the device", async ({ page }) => {
  await pluginsPage(page);
  const rogue = card(page, "Rogue Leak");
  await rogue.getByRole("button", { name: "Approve access" }).click();
  await pressConfirm(page, "Approve and start");

  await expect(rogue.getByText("Blocked 1 attempt")).toBeVisible();
  await expect(rogue.getByText("It tried to do something it wasn't allowed to.")).toBeVisible();
  await expect(rogue.getByText("the secret setting apiKey can't be sent to 10.77.0.20:9000: its sendTo doesn't list it")).toBeVisible();

  const { requests } = await deviceState();
  expect(requests.map((r) => r.url)).toEqual(["/honest-report"]);
  expect(JSON.stringify(requests)).not.toContain(KEY);
  await expect(page.getByRole("main")).not.toContainText(KEY);
  expect(await (await api.get("/api/extensions")).text()).not.toContain(KEY);
});

test("where it has connected lists each destination it really used", async ({ page }) => {
  await approve(api, "./extensions/rogue-leak");
  await pluginsPage(page);
  const rogue = card(page, "Rogue Leak");
  const summary = rogue.getByText("Where it has connected (1)");
  await expect(summary).toBeVisible();
  await summary.click();
  await expect(rogue.getByText("http GET 10.77.0.20:9000/honest-report")).toBeVisible();
  await expect(rogue.getByText(/1 time, last/)).toBeVisible();
});

test("ten violations stop an extension and take its approval back; approving again clears them", async ({ page }) => {
  await pluginsPage(page);
  const flood = card(page, "Rogue Flood");
  await flood.getByRole("button", { name: "Approve access" }).click();
  await pressConfirm(page, "Approve and start");

  await expect(flood.getByText(/Blocked \d+ attempts?/)).toBeVisible();
  await expect(flood.getByText("network: not-listed.invalid isn't in this extension's network list").first()).toBeVisible();
  await expect(flood.getByText(/Stopped after 10 violations of its declared access/)).toBeVisible({ timeout: 45_000 });
  await expect(flood.getByText("Problem", { exact: true })).toBeVisible();
  await expect(flood.getByText("Blocked 10 attempts")).toBeVisible();
  await expect(flood.getByText("Waiting for your OK")).toBeVisible();
  // It stays stopped: the count doesn't keep climbing.
  await expect(flood.getByText("Blocked 11 attempts")).toHaveCount(0);

  await flood.getByRole("button", { name: "Approve access" }).click();
  await pressConfirm(page, "Approve and start");
  await expect(flood.getByText(/Stopped after 10 violations/)).toBeHidden();
  await expect(flood.getByText("Running", { exact: true })).toBeVisible();
});

test("a secret downgraded after approval stops the extension and hides the value", async ({ page }) => {
  const VALUE = "the-real-service-key";
  const TYPED = "typed-into-the-plain-field";
  await pluginsPage(page);
  await page.getByRole("button", { name: /^Add plugin/ }).click();
  await page.getByRole("menuitem", { name: /From local files/ }).click();
  const add = page.getByRole("dialog");
  await add.getByLabel("File for the plugin").setInputFiles(join(uploads, "key-keeper-v1.ts"));
  await add.getByLabel("Service key").fill(VALUE);
  await add.getByRole("button", { name: /Allow this access and add/ }).click();
  await expect(add).toBeHidden();
  const keeper = card(page, "Key Keeper");
  await expect(keeper.getByText("Running", { exact: true })).toBeVisible();
  await expect(keeper.getByText("Configured: Service key")).toBeVisible();

  await keeper.getByLabel("File with the new version").setInputFiles(join(uploads, "key-keeper-v2.ts"));
  // The old value isn't carried into a setting that would show it: the update has to ask again, and warns.
  const update = page.getByRole("dialog", { name: /Replace Key Keeper/ });
  await expect(update.getByText("doesn't treat it as a secret")).toBeVisible();
  await expect(update.getByLabel("Service key")).toHaveValue("");
  await expect(update.getByRole("button", { name: "Replace" })).toBeDisabled();
  await update.getByLabel("Service key").fill(TYPED);
  await update.getByRole("button", { name: "Replace" }).click();

  await expect(keeper.getByText("Needs your OK")).toBeVisible();
  await expect(keeper.getByText("You approved the setting apiKey as a secret, and this code no longer treats it as one.")).toBeVisible();
  await expect(keeper.getByText("the setting apiKey was approved as a secret and is no longer declared as one")).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText(VALUE);
  const listed = await (await api.get("/api/extensions")).text();
  expect(listed).not.toContain(VALUE);
  expect(listed).not.toContain(TYPED);
  await expect(page.getByRole("main")).not.toContainText(TYPED);

  await keeper.getByRole("button", { name: "Edit settings" }).click();
  const edit = page.getByRole("dialog", { name: "Settings for Key Keeper" });
  await expect(edit.getByLabel("Service key")).toHaveValue("");
});

test("a scan warning shows before anything runs, and generated code is blocked anyway", async ({ page }) => {
  await pluginsPage(page);
  await page.getByRole("button", { name: /^Add plugin/ }).click();
  await page.getByRole("menuitem", { name: /From local files/ }).click();
  const add = page.getByRole("dialog");
  await add.getByLabel("File for the plugin").setInputFiles(join(uploads, "evil-scan.ts"));
  await expect(add.getByText("Check this before you go on")).toBeVisible();
  await expect(add.getByText(/eval/)).toBeVisible();
  await add.getByRole("button", { name: /Allow this access and add/ }).click();
  await expect(add).toBeHidden();
  const warned = card(page, "Scan Warned");
  await expect(warned.getByText("Check this before you go on")).toBeVisible();
  await expect(warned.getByText(/Blocked \d+ attempts?/)).toBeVisible();
  await expect(warned.getByText(/dynamic-code/).first()).toBeVisible();
});
