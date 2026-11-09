import { expect, test, type Page } from "@playwright/test";
import { addPerson, approve, FAKE_HOME, nav, open, OWNER, ownerApi, person, signOut } from "../support/home";
import { approveListing, freshHome, marketOn, marketReset } from "../support/stack";

test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });

test.beforeAll(async () => {
  await freshHome({ config: "everything" });
  await marketReset();
  await approveListing("market-lamp", "v1.0.0");
  const api = await ownerApi();
  await approve(api, FAKE_HOME);
  await approve(api, "./extensions/fake-data");
  await approve(api, "./extensions/rogue-leak");
  await addPerson(api, person("viewer"));
  await expect.poll(async () => (await (await api.get("/api/devices")).json()).length).toBeGreaterThan(0);
  await api.post("/api/tokens", { data: { name: "A token with quite a long name to wrap", role: "viewer" } });
});

/** Neither the page nor its scrolling main area may scroll sideways. A table may, inside its own frame. */
async function expectNoSidewaysScroll(page: Page) {
  const overflow = () =>
    page.evaluate(() => {
      const main = document.querySelector("main")!;
      return {
        page: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        body: document.body.scrollWidth - document.documentElement.clientWidth,
        main: main.scrollWidth - main.clientWidth,
      };
    });
  await expect.poll(overflow).toEqual({ page: 0, body: 0, main: 0 });
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
  test(`no sideways scroll at 375px on ${path}`, async ({ page }) => {
    await open(page, OWNER, path);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    // The page has loaded once its first control shows. Home's are links.
    await expect(page.getByRole("main").getByRole("button").or(page.getByRole("main").getByRole("link")).first()).toBeVisible();
    await expectNoSidewaysScroll(page);
  });
}

test("no sideways scroll at 375px in the script editor", async ({ page }) => {
  await open(page, OWNER, "/automations/lights");
  await expect(page.locator(".monaco-editor .view-lines")).toContainText("fake-home:lamp");
  await expectNoSidewaysScroll(page);
});

test("no sideways scroll at 375px on the sign-in and first-run screens", async ({ page, context }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  await expectNoSidewaysScroll(page);
  await context.clearCookies();
});

test("the sidebar is a menu you open, which closes after choosing", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await expect(nav(page)).toHaveCount(0);
  await page.getByRole("button", { name: "Open the menu" }).click();
  const menu = page.getByRole("dialog", { name: "Sidebar" });
  await expect(menu).toBeVisible();
  await menu.getByRole("link", { name: "Plugins" }).click();
  await expect(page).toHaveURL(/\/plugins$/);
  await expect(menu).toBeHidden();
  await expect(page.getByRole("heading", { level: 1, name: "Plugins" })).toBeVisible();
});

test("from the phone menu you can reach your account and sign out", async ({ page }) => {
  await open(page, person("viewer"), "/devices");
  await page.getByRole("button", { name: "Open the menu" }).click();
  await expect(page.getByRole("dialog", { name: "Sidebar" }).getByRole("link", { name: "People" })).toHaveCount(0);
  await signOut(page, "viewer");
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
});

test("controls stay big enough to press and a device still responds", async ({ page }) => {
  await open(page, OWNER, "/devices");
  const power = page.getByRole("switch", { name: "Desk lamp Power" });
  await power.tap();
  await expect(power).toBeChecked();
  const box = await page.getByRole("button", { name: "Raise Hall thermostat" }).boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(40);
});

test("the install dialog fits a phone", async ({ page }) => {
  test.skip(!marketOn, "this copy has no marketplace service");
  await open(page, OWNER, "/marketplace/market-lamp");
  await page.getByRole("button", { name: "Install Market Lamp" }).click();
  const dialog = page.getByRole("dialog", { name: "Add Market Lamp" });
  await expect(dialog.getByRole("button", { name: /Allow this access/ })).toBeVisible();
  const box = await dialog.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(375);
});
