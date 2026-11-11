import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { join } from "node:path";
import { addFolder, approve, card, open, OWNER, ownerApi, pressConfirm } from "../support/home";
import { deviceState, freshHome, storedSettings, uploads } from "../support/stack";

const KEY = "e2e-key-that-must-not-leave";
let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "security" });
  api = await ownerApi();
});

const pluginsPage = (page: Page) => open(page, OWNER, "/plugins");

const addRogue = async (page: Page) => {
  const dialog = await addFolder(page, "plugin", "./extensions/rogue-leak");
  await dialog.getByLabel("Device address").fill("10.77.0.20:9000");
  await dialog.getByLabel("API key").fill(KEY);
  return dialog;
};

test("a rogue extension's risk shows before it is added, and nothing runs until it is", async ({ page }) => {
  await pluginsPage(page);
  const dialog = await addRogue(page);
  await expect(dialog.getByText("The risk in approving this")).toBeVisible();
  await expect(dialog).toContainText("It holds your API key and can also send data to the address in the setting host.");
  await expect(dialog).toContainText("A rogue extension could steal it this way");
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("heading", { name: "No plugins yet" })).toBeVisible();
  await expect.poll(async () => (await deviceState()).requests.length).toBe(0);
});

test("a key sent as written is refused and shown, and nothing reaches the device", async ({ page }) => {
  await pluginsPage(page);
  const dialog = await addRogue(page);
  await dialog.getByRole("button", { name: "Allow this access and add Rogue Leak" }).click();
  await expect(dialog).toBeHidden();
  const rogue = card(page, "Rogue Leak");
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
  await approve(api, "./extensions/rogue-flood");
  await pluginsPage(page);
  const flood = card(page, "Rogue Flood");

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

test("a credentials file chosen on the page reaches the extension, and can't be read back anywhere, the database included", async ({ page }) => {
  const FILE_KEYS = ["file-key-that-must-stay-hidden", "another-hidden-file-key"];
  await pluginsPage(page);
  const dialog = await addFolder(page, "plugin", "./extensions/file-plugin");
  await expect(dialog).toContainText("Holds the file you give it for: Devices file");
  await dialog.getByLabel("Devices file").setInputFiles(join(uploads, "devices.json"));
  await expect(dialog.getByText("devices.json is read and will be kept encrypted.")).toBeVisible();
  await dialog.getByRole("button", { name: "Allow this access and add File Plugin" }).click();
  await expect(dialog).toBeHidden();
  const plugin = card(page, "File Plugin");
  await expect(plugin.getByText("Running", { exact: true })).toBeVisible();
  await plugin.getByRole("button", { name: "Details for scripts" }).click();
  await expect(plugin.getByText('"devices": 2')).toBeVisible();

  await plugin.getByRole("button", { name: "Edit settings" }).click();
  const edit = page.getByRole("dialog", { name: "Settings for File Plugin" });
  await expect(edit.getByText("Set. Choose a file only to replace it.")).toBeVisible();
  await edit.getByRole("button", { name: "Save" }).click();
  await expect(edit).toBeHidden();
  await expect(plugin.getByText("Running", { exact: true })).toBeVisible();

  const listed = await (await api.get("/api/extensions")).text();
  const stored = await storedSettings();
  expect(stored.trim()).not.toBe("");
  for (const key of FILE_KEYS) {
    await expect(page.getByRole("main")).not.toContainText(key);
    expect(listed).not.toContain(key);
    expect(stored).not.toContain(key);
  }
});
