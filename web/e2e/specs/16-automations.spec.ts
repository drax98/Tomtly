import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { addPerson, approveEverything, card, confirmDialog, open, OWNER, ownerApi, peopleAndDevices, person, pressConfirm } from "../support/home";
import { automationsDir, deviceValue, freshHome, restartService } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome();
  api = await ownerApi();
  await peopleAndDevices(api);
});

const activity = (page: Page) => page.getByRole("region", { name: "Activity" });
const approvalFor = (page: Page) => card(page, "Lights").getByRole("button", { name: /^Approve: / });
const liveSwitch = (page: Page) => page.getByRole("switch", { name: "Run lights live" });

async function approveLights(page: Page) {
  const lights = card(page, "Lights");
  await expect(lights.getByText("Needs your approval before it goes live")).toBeVisible();
  await approveEverything(lights);
  await expect(lights.getByText("Needs your approval before it goes live")).toBeHidden();
}

test("the list shows each script, practising, with its problems", async ({ page }) => {
  await open(page, OWNER, "/automations");
  await expect(page.getByRole("heading", { level: 3, name: "Lights" })).toBeVisible();
  await expect(card(page, "Lights").getByText("Practising")).toBeVisible();
  await expect(liveSwitch(page)).not.toBeChecked();
  await expect(card(page, "Lights").getByText("Shows what it would do, changes nothing.")).toBeVisible();

  const broken = card(page, "Broken");
  await expect(broken.getByText("This automation has a problem")).toBeVisible();
  await expect(broken.getByText(/broken on purpose/)).toBeVisible();
});

test("approvals are per script, refused calls are counted and logged, and a practice run changes nothing", async ({ page }) => {
  await open(page, OWNER, "/automations");
  const lights = card(page, "Lights");
  await expect(lights.getByText(/refused \d+ times?/)).toBeVisible();
  await expect(activity(page).getByText("Refused").first()).toBeVisible();

  await approveLights(page);
  await expect(activity(page).getByText("Would do").first()).toBeVisible();
  await expect(activity(page).getByText(/fake-home:lamp|Desk lamp/).first()).toBeVisible();
  expect(await deviceValue("lamp", "switch")).toBe(false);

  await lights.getByText(/Approved access \(\d+\)/).click();
  await lights.getByRole("button", { name: /^Withdraw: / }).first().click();
  await expect(lights.getByText("Needs your approval before it goes live")).toBeVisible();
});

test("going live asks first, then really controls the device; switching back offers undo", async ({ page }) => {
  await open(page, OWNER, "/automations");
  await approveLights(page);

  await liveSwitch(page).click();
  const asked = confirmDialog(page);
  await expect(asked).toContainText('Let "Lights" control your home?');
  await asked.getByRole("button", { name: "Cancel" }).click();
  await expect(liveSwitch(page)).not.toBeChecked();
  expect(await deviceValue("lamp", "switch")).toBe(false);

  await liveSwitch(page).click();
  await pressConfirm(page, "Yes, switch it on");
  await expect(liveSwitch(page)).toBeChecked();
  await expect(card(page, "Lights").getByText("It is controlling your devices right now.")).toBeVisible();
  await expect.poll(() => deviceValue("lamp", "switch")).toBe(true);
  await expect(activity(page).getByText("Did").first()).toBeVisible();

  await restartService();
  await page.reload();
  await expect(liveSwitch(page)).toBeChecked();

  await liveSwitch(page).click();
  await expect(page.getByText("Lights is back to practising")).toBeVisible();
  await expect(liveSwitch(page)).not.toBeChecked();
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(liveSwitch(page)).toBeChecked();
});

test("going live with something unapproved warns, and those calls stay refused", async ({ page }) => {
  await open(page, OWNER, "/automations");
  await expect(card(page, "Lights").getByText("Needs your approval before it goes live")).toBeVisible();
  await liveSwitch(page).click();
  await expect(confirmDialog(page)).toContainText("It isn't approved for these, so they will be refused");
  await pressConfirm(page, "Yes, switch it on");
  await expect(card(page, "Lights").getByText("Running, but these calls are refused")).toBeVisible();
  expect(await deviceValue("lamp", "switch")).toBe(false);
});

test("reloading from disk picks up a new file and a changed one", async ({ page }) => {
  await open(page, OWNER, "/automations");
  await expect(page.getByRole("heading", { level: 3, name: "Lights" })).toBeVisible();
  writeFileSync(join(automationsDir, "from-disk.ts"), 'home.log("written on disk");\n');
  await page.getByRole("button", { name: "Reload from disk" }).click();
  await expect(page.getByRole("heading", { level: 3, name: "From-disk" })).toBeVisible();

  writeFileSync(join(automationsDir, "from-disk.ts"), 'throw new Error("now it fails");\n');
  await page.getByRole("button", { name: "Reload from disk" }).click();
  await expect(card(page, "From-disk").getByText(/now it fails/)).toBeVisible();
});

test("a new script: its name is checked and it starts from an example", async ({ page }) => {
  await open(page, OWNER, "/automations");
  const name = page.getByRole("textbox", { name: "New script name" });
  const create = page.getByRole("button", { name: "New script" });
  await expect(create).toBeDisabled();
  await name.fill("bad name!");
  await expect(page.getByText("Use letters, digits, - and _.")).toBeVisible();
  await name.fill("lights");
  await expect(page.getByText("That name is taken.")).toBeVisible();
  await expect(create).toBeDisabled();
  await name.fill("evening");
  await create.click();
  await expect(page.getByRole("heading", { name: "evening.ts" })).toBeVisible();
  await expect(page.locator(".monaco-editor .view-lines")).toContainText("A script is ordinary TypeScript");
});

test("with no scripts the page says how to start, by role", async ({ page }) => {
  await freshHome({ automations: "empty" });
  const owner = await ownerApi();
  await addPerson(owner, person("viewer"));
  await open(page, OWNER, "/automations");
  await expect(page.getByRole("heading", { name: "No automations yet" })).toBeVisible();
  await page.getByRole("button", { name: "Write your first one" }).click();
  await expect(page.getByRole("textbox", { name: "New script name" })).toBeFocused();
  await expect(page.getByText("Nothing yet. Each decision an automation makes will appear here")).toBeVisible();

  const viewer = await page.context().browser()!.newContext({ baseURL: "http://localhost:8099" });
  const theirs = await viewer.newPage();
  await open(theirs, person("viewer"), "/automations");
  await expect(theirs.getByText("Ask an editor or administrator to add one.")).toBeVisible();
  await viewer.close();
});
