import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { appendToEditor, approveEverything, card, confirmDialog, fillEditor, open, OWNER, ownerApi, peopleAndDevices, person, pressConfirm } from "../support/home";
import { automationsDir, deviceValue, freshHome } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome();
  api = await ownerApi();
  await peopleAndDevices(api);
});

const scriptFile = (name: string) => readFileSync(join(automationsDir, `${name}.ts`), "utf8");
const editorText = (page: Page) => page.locator(".monaco-editor .view-lines");
const save = (page: Page, name = "Save") => page.getByRole("button", { name, exact: true });

async function openLights(page: Page) {
  await open(page, OWNER, "/automations");
  await page.getByRole("button", { name: "Edit lights" }).click();
  await expect(editorText(page)).toContainText("fake-home:lamp");
}

test("editing a script: dirty state, saving to the file, and back", async ({ page }) => {
  await openLights(page);
  await expect(page.getByText("Shadow", { exact: true })).toBeVisible();
  await expect(save(page)).toBeDisabled();
  await appendToEditor(page, "\n// a saved comment\n");
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await save(page).click();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();
  await expect(page.getByText("Unsaved changes")).toBeHidden();
  expect(scriptFile("lights")).toContain("// a saved comment");
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page).toHaveURL(/\/automations$/);
  await expect(confirmDialog(page)).toHaveCount(0);
});

test("Ctrl+S saves the safe way", async ({ page }) => {
  await openLights(page);
  await appendToEditor(page, "\n// saved with the keyboard\n");
  await page.keyboard.press("Control+S");
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();
  expect(scriptFile("lights")).toContain("// saved with the keyboard");
});

test("leaving with unsaved changes asks first", async ({ page }) => {
  await openLights(page);
  await appendToEditor(page, "\n// never saved\n");
  await page.getByRole("button", { name: "Back" }).click();
  await expect(confirmDialog(page)).toContainText("Your changes to lights will be lost.");
  await confirmDialog(page).getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await page.getByRole("button", { name: "Back" }).click();
  await pressConfirm(page, "Discard changes");
  await expect(page).toHaveURL(/\/automations$/);
  expect(scriptFile("lights")).not.toContain("// never saved");
});

test("a script that doesn't compile or throws is refused and nothing changes", async ({ page }) => {
  await openLights(page);
  const before = scriptFile("lights");
  await fillEditor(page, "const = ;");
  await save(page).click();
  await expect(page.getByText("Nothing was saved")).toBeVisible();
  expect(scriptFile("lights")).toBe(before);

  await fillEditor(page, 'throw new Error("fails while loading");');
  await save(page).click();
  await expect(page.getByText("Nothing was saved")).toBeVisible();
  await expect(page.getByText(/fails while loading/)).toBeVisible();
  expect(scriptFile("lights")).toBe(before);
});

test("someone else's change on disk isn't overwritten", async ({ page }) => {
  await openLights(page);
  await appendToEditor(page, "\n// mine\n");
  const theirs = 'home.log("edited elsewhere");\n';
  writeFileSync(join(automationsDir, "lights.ts"), theirs);
  await save(page).click();
  await expect(page.getByText("Someone else changed this script")).toBeVisible();
  expect(scriptFile("lights")).toBe(theirs);

  await page.getByRole("button", { name: "Load the latest version" }).click();
  await pressConfirm(page, "Load it");
  await expect(editorText(page)).toContainText("edited elsewhere");
  await expect(page.getByText("Someone else changed this script")).toBeHidden();
});

test("saving a new request says it isn't approved yet", async ({ page }) => {
  await openLights(page);
  await fillEditor(page, 'const fan = home.device("fake-home:thermostat");\nhome.reconcile(() => { fan.get("switch"); });\n');
  await save(page).click();
  await expect(page.getByText("Saved, but it isn't approved for everything it asks for")).toBeVisible();
});

test("saving a live script: back to shadow unless you choose otherwise", async ({ page }) => {
  await open(page, OWNER, "/automations");
  const lights = card(page, "Lights");
  await expect(lights.getByRole("button", { name: /^Approve: / }).first()).toBeVisible();
  await approveEverything(lights);
  await page.getByRole("switch", { name: "Run lights live" }).click();
  await pressConfirm(page, "Yes, switch it on");
  await expect.poll(() => deviceValue("lamp", "switch")).toBe(true);

  await page.getByRole("button", { name: "Edit lights" }).click();
  await expect(editorText(page)).toContainText("fake-home:lamp");
  await expect(page.getByText("Live", { exact: true })).toBeVisible();
  await expect(page.getByText("This automation is controlling real devices.")).toBeVisible();

  await appendToEditor(page, "\n// first edit\n");
  await save(page, "Save and keep live").click();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();
  await expect(page.getByText("Live", { exact: true })).toBeVisible();

  await appendToEditor(page, "\n// second edit\n");
  await save(page, "Save to shadow").click();
  await expect(page.getByText("Saved. The script is back in shadow mode.")).toBeVisible();
  await expect(page.getByText("Shadow", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page.getByRole("switch", { name: "Run lights live" })).not.toBeChecked();
});

test("an editor's change takes approvals back, and can't keep a script live", async ({ page, browser }) => {
  await open(page, OWNER, "/automations");
  const lights = card(page, "Lights");
  await expect(lights.getByRole("button", { name: /^Approve: / }).first()).toBeVisible();
  await approveEverything(lights);
  await page.getByRole("switch", { name: "Run lights live" }).click();
  await pressConfirm(page, "Yes, switch it on");
  await expect(page.getByRole("switch", { name: "Run lights live" })).toBeChecked();

  const context = await browser.newContext({ baseURL: "http://localhost:8099" });
  const editor = await context.newPage();
  await open(editor, person("editor"), "/automations/lights");
  await expect(editorText(editor)).toContainText("fake-home:lamp");
  await expect(editor.getByText("Live", { exact: true })).toBeVisible();
  await appendToEditor(editor, "\n// changed by the editor\n");
  await expect(save(editor, "Save and keep live")).toHaveCount(0);
  await save(editor, "Save to shadow").click();
  await expect(editor.getByText("Saved. The script is back in shadow mode.")).toBeVisible();
  await context.close();

  await expect(page.getByRole("switch", { name: "Run lights live" })).not.toBeChecked();
  await expect(lights.getByText("Needs your approval before it goes live")).toBeVisible();
});

test("a new script is saved as a file and then runs in practice", async ({ page }) => {
  await open(page, OWNER, "/automations");
  await page.getByRole("textbox", { name: "New script name" }).fill("evening");
  await page.getByRole("button", { name: "New script" }).click();
  await expect(editorText(page)).toContainText("A script is ordinary TypeScript");
  await fillEditor(page, 'home.log("evening started");\n');
  await save(page).click();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();
  expect(scriptFile("evening")).toContain("evening started");
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page.getByRole("heading", { level: 3, name: "Evening" })).toBeVisible();
  await expect(card(page, "Evening").getByText("Practising")).toBeVisible();
});

test("an editor can open and save scripts; family can't reach it", async ({ page, browser }) => {
  await open(page, person("editor"), "/automations/lights");
  await expect(editorText(page)).toContainText("fake-home:lamp");
  await appendToEditor(page, "\n// by the editor\n");
  await save(page).click();
  await expect(page.getByText("Saved", { exact: true })).toBeVisible();

  const family = await browser.newContext({ baseURL: "http://localhost:8099" });
  const theirs = await family.newPage();
  await open(theirs, person("operator"), "/automations/lights");
  await appendToEditor(theirs, "\n// by family\n");
  await save(theirs).click();
  await expect(theirs.getByText("Nothing was saved")).toBeVisible();
  await expect(theirs.getByText("you need the automations.edit permission")).toBeVisible();
  await family.close();
});
