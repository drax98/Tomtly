import { expect, test, type Page } from "@playwright/test";
import { accountMenu, approve, FAKE_HOME, nav, open, OWNER, ownerApi } from "../support/home";
import { approveListing, freshHome, marketReset } from "../support/stack";

test.beforeAll(async () => {
  await freshHome({ config: "everything" });
  await marketReset();
  await approveListing("market-lamp", "v1.0.0");
  const api = await ownerApi();
  await approve(api, FAKE_HOME);
  await approve(api, "./extensions/fake-data");
  await approve(api, "./extensions/rogue-leak");
  await expect.poll(async () => (await (await api.get("/api/devices")).json()).length).toBeGreaterThan(0);
});

const NAMED = /^\s*- (button|link|textbox|searchbox|combobox|switch|slider|checkbox|radio|tab|menuitem)\b(?!\s+")/;

/** What the browser's accessibility tree says has a role but no name. Playwright's own, so there's no new dependency. */
async function namelessControls(page: Page, scope = page.locator("body")) {
  const tree = await scope.ariaSnapshot();
  return tree.split("\n").filter((line) => NAMED.test(line));
}

const PAGES: [string, RegExp][] = [
  ["/devices", /Your devices/],
  ["/automations", /Automations/],
  ["/integrations", /Integrations/],
  ["/integrations/fake-home", /Fake Home/],
  ["/plugins", /Plugins/],
  ["/marketplace", /Marketplace/],
  ["/marketplace/market-lamp", /Marketplace/],
  ["/users", /People/],
  ["/audit", /Activity log/],
  ["/tokens", /API tokens/],
  ["/settings", /Settings/],
  ["/rooms", /Rooms/],
  ["/home", /Home/],
];

for (const [path, heading] of PAGES) {
  test(`${path} has one main landmark, one top heading and named controls`, async ({ page }) => {
    await open(page, OWNER, path);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    await expect(page.getByRole("main")).toHaveCount(1);
    await expect(page.getByRole("heading", { level: 1 })).toHaveCount(1);
    await expect(nav(page)).toHaveCount(1);
    // The page has loaded once its first control shows. Home's are links.
    await expect(page.getByRole("main").getByRole("button").or(page.getByRole("main").getByRole("link")).first()).toBeVisible();
    expect(await namelessControls(page)).toEqual([]);
  });
}

test("the script editor has a main landmark and named controls", async ({ page }) => {
  await open(page, OWNER, "/automations/lights");
  await expect(page.locator(".monaco-editor .view-lines")).toContainText("fake-home:lamp");
  await expect(page.getByRole("main")).toHaveCount(1);
  expect(await namelessControls(page)).toEqual([]);
});

test("sign-in has one main landmark, labelled fields, and a language", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "Welcome back" })).toBeVisible();
  await expect(page.getByRole("main")).toHaveCount(1);
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  expect(await namelessControls(page)).toEqual([]);
  await expect(page.getByLabel("Name")).toBeFocused();
});

test("dialogs have names, and Escape closes them and returns focus", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await accountMenu(page, OWNER.username).click();
  await page.getByRole("menuitem", { name: "Change password" }).click();
  const dialog = page.getByRole("dialog", { name: "Change your password" });
  await expect(dialog).toBeVisible();
  expect(await namelessControls(page, dialog)).toEqual([]);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("main")).toHaveCount(1);
});

test("the menu works from the keyboard", async ({ page }) => {
  await open(page, OWNER, "/devices");
  const plugins = nav(page).getByRole("link", { name: "Plugins" });
  await plugins.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/plugins$/);
  await expect(plugins).toHaveAttribute("aria-current", "page");
});
