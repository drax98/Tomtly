import { expect, test, type Page } from "@playwright/test";
import { addPerson, apiFromBrowser, approve, card, chooseOption, FAKE_HOME, nav, open, OWNER, ownerApi, peopleAndDevices, person } from "../support/home";
import { deviceValue, freshHome } from "../support/stack";

const WRITE = "/api/devices/fake-home:lamp/dps/switch";
let switchDp = "";

test.beforeEach(async () => {
  await freshHome();
  const api = await ownerApi();
  await peopleAndDevices(api);
  const devices = await (await api.get("/api/devices")).json();
  switchDp = devices.find((d: { id: string }) => d.id === "fake-home:lamp").dps.find((d: { code: string }) => d.code === "switch").dp;
});

const write = () => WRITE.replace("switch", switchDp);
const lampSwitch = (page: Page) => page.getByRole("switch", { name: "Desk lamp Power" });

async function refusals(page: Page, calls: [string, string, unknown?][]) {
  for (const [method, path, body] of calls) expect(await apiFromBrowser(page, method, path, body), `${method} ${path}`).toBe(403);
}

const ADMIN_ONLY: [string, string, unknown?][] = [
  ["GET", "/api/users"],
  ["POST", "/api/users", { username: "someone", password: "a long enough password", role: "admin" }],
  ["GET", "/api/audit"],
  ["POST", "/api/extensions", { source: "./extensions/folder-plugin", settings: {} }],
  ["POST", "/api/extensions/approve", { source: FAKE_HOME }],
  ["DELETE", `/api/extensions?source=${encodeURIComponent(FAKE_HOME)}`],
  ["DELETE", "/api/devices/fake-home:lamp"],
  ["POST", "/api/integrations/fake-home/discover", {}],
  ["POST", "/api/automations/lights/access", { keys: ["x"], approved: true }],
];

test("a viewer can look at everything and change nothing", async ({ page }) => {
  await open(page, person("viewer"), "/devices");
  await expect(page.getByRole("button", { name: "Account menu for viewer" })).toContainText("Viewer");
  await expect(page.getByText("only change things with the Family role")).toBeVisible();
  await expect(nav(page).getByRole("link", { name: "People" })).toHaveCount(0);
  await expect(nav(page).getByRole("link", { name: "Activity log" })).toHaveCount(0);
  await expect(lampSwitch(page)).toBeDisabled();
  await expect(page.getByRole("button", { name: "Lower Hall thermostat" })).toBeDisabled();
  await expect(page.getByRole("slider", { name: "Desk lamp Brightness" })).toHaveAttribute("data-disabled", "");
  await expect(page.getByRole("slider", { name: "Bedroom blinds position" })).toHaveAttribute("data-disabled", "");
  await expect(page.getByRole("button", { name: "Start cleaning" })).toBeDisabled();

  await page.getByText("Desk lamp", { exact: true }).hover();
  await expect(page.getByRole("tooltip").filter({ hasText: "Viewers can look but not change" })).toBeVisible();

  await nav(page).getByRole("link", { name: "Automations" }).click();
  await expect(page.getByText("Editing needs the Editor role, and switching them on needs it too.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Edit lights" })).toHaveCount(0);
  await expect(page.getByRole("switch", { name: "Run lights live" })).toBeDisabled();
  await expect(page.getByRole("textbox", { name: "New script name" })).toHaveCount(0);
  await expect(page.getByText("An administrator has to approve these.").first()).toBeVisible();

  await nav(page).getByRole("link", { name: "Integrations" }).click();
  await expect(page.getByText("Adding or removing integrations needs an administrator.")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Add integration/ })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0);

  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect(page.getByText("Removing devices or looking for new ones needs an administrator.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove Desk lamp" })).toHaveCount(0);

  await nav(page).getByRole("link", { name: "Plugins" }).click();
  await expect(page.getByText("Adding or removing plugins needs an administrator.")).toBeVisible();
  await nav(page).getByRole("link", { name: "Marketplace" }).click();
  await expect(page.getByText("Installing needs an administrator. You can look around.")).toBeVisible();
});

test("the service itself refuses a viewer, whatever the page shows", async ({ page }) => {
  await open(page, person("viewer"), "/devices");
  await refusals(page, [
    ["POST", write(), { value: true }],
    ["POST", "/api/automations/lights/live", { live: true }],
    ["POST", "/api/automations/lights/source", { source: "", version: "" }],
    ["POST", "/api/automations/reload", {}],
    ...ADMIN_ONLY,
  ]);
  expect(await deviceValue("lamp", "switch")).toBe(false);
});

test("reload from disk is refused for a viewer with a clear message", async ({ page }) => {
  await open(page, person("viewer"), "/automations");
  await page.getByRole("button", { name: "Reload from disk" }).click();
  await expect(page.getByText("That didn't work")).toBeVisible();
  await expect(page.getByText("you need the automations.edit permission")).toBeVisible();
});

test("family can control devices and nothing else", async ({ page }) => {
  await open(page, person("operator"), "/devices");
  await expect(page.getByRole("button", { name: "Account menu for operator" })).toContainText("Family");
  await expect(page.getByText("Tap a switch to turn something on or off.")).toBeVisible();
  await lampSwitch(page).click();
  await expect(lampSwitch(page)).toBeChecked();
  await expect.poll(() => deviceValue("lamp", "switch")).toBe(true);

  await expect(nav(page).getByRole("link", { name: "People" })).toHaveCount(0);
  await nav(page).getByRole("link", { name: "Automations" }).click();
  await expect(page.getByRole("button", { name: "Edit lights" })).toHaveCount(0);
  await expect(page.getByRole("switch", { name: "Run lights live" })).toBeDisabled();
  await refusals(page, [
    ["POST", "/api/automations/lights/live", { live: true }],
    ["POST", "/api/automations/lights/source", { source: "", version: "" }],
    ...ADMIN_ONLY,
  ]);
});

test("an editor can write automations and switch them on, but not approve or manage", async ({ page }) => {
  await open(page, person("editor"), "/automations");
  await expect(page.getByRole("button", { name: "Account menu for editor" })).toContainText("Editor");
  await expect(page.getByRole("button", { name: "Edit lights" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "New script name" })).toBeVisible();
  await expect(page.getByRole("switch", { name: "Run lights live" })).toBeEnabled();
  await expect(page.getByRole("button", { name: /^Approve: / })).toHaveCount(0);
  await expect(page.getByText("An administrator has to approve these.").first()).toBeVisible();

  await page.getByRole("button", { name: "Reload from disk" }).click();
  await expect(page.getByText("That didn't work")).toHaveCount(0);

  await nav(page).getByRole("link", { name: "Integrations" }).click();
  await expect(page.getByRole("button", { name: /^Add integration/ })).toHaveCount(0);
  await refusals(page, ADMIN_ONLY);
  await expect(nav(page).getByRole("link", { name: "People" })).toHaveCount(0);
});

test("an administrator sees the administration pages and every control", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await expect(nav(page).getByRole("link", { name: "People" })).toBeVisible();
  await expect(nav(page).getByRole("link", { name: "Activity log" })).toBeVisible();
  await nav(page).getByRole("link", { name: "Integrations" }).click();
  await expect(page.getByRole("button", { name: /^Add integration/ })).toBeVisible();
  await card(page, "Fake Home").getByRole("button", { name: "Open" }).click();
  await expect(page.getByRole("button", { name: "Remove", exact: true })).toBeVisible();
  await nav(page).getByRole("link", { name: "Automations" }).click();
  await expect(page.getByRole("button", { name: /^Approve: / }).first()).toBeVisible();
});

test("taking away a role ends that person's session on the very next request", async ({ page, browser }) => {
  await open(page, person("operator"), "/devices");
  await expect(lampSwitch(page)).toBeEnabled();

  const admin = await browser.newPage();
  await open(admin, OWNER, "/users");
  await chooseOption(admin, admin.getByRole("combobox", { name: "Role of operator" }), /^Viewer/);
  await expect(admin.getByRole("combobox", { name: "Role of operator" })).toHaveText("Viewer");

  expect(await apiFromBrowser(page, "POST", write(), { value: true })).toBe(401);
  await expect(page.getByRole("heading", { name: "Sign in again to continue" })).toBeVisible();
  await page.getByLabel("Name").fill("operator");
  await page.getByLabel("Password").fill(person("operator").password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(lampSwitch(page)).toBeDisabled();
  expect(await apiFromBrowser(page, "POST", write(), { value: true })).toBe(403);
  expect(await deviceValue("lamp", "switch")).toBe(false);
  await admin.close();
});

test("a token never does more than the person who made it", async ({ page }) => {
  await open(page, person("viewer"), "/tokens");
  await page.getByRole("combobox", { name: "Token role" }).click();
  await expect(page.getByRole("option")).toHaveCount(1);
  await expect(page.getByRole("option", { name: /^Viewer/ })).toBeVisible();
  await page.keyboard.press("Escape");
  const asked = await apiFromBrowser(page, "POST", "/api/tokens", { name: "sneaky", role: "admin" });
  expect(asked).toBe(403);
});

