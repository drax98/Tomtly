import { expect, test } from "@playwright/test";
import { addPerson, approve, FAKE_HOME, nav, open, OWNER, ownerApi, person } from "../support/home";
import { freshHome } from "../support/stack";

test.beforeAll(async () => {
  await freshHome();
  const api = await ownerApi();
  await addPerson(api, person("viewer"));
  await approve(api, FAKE_HOME);
});

const PAGES: [string, string, string][] = [
  ["Devices", "/devices", "Your devices"],
  ["Automations", "/automations", "Automations"],
  ["Integrations", "/integrations", "Integrations"],
  ["Plugins", "/plugins", "Plugins"],
  ["Marketplace", "/marketplace", "Marketplace"],
  ["People", "/users", "People"],
  ["Activity log", "/audit", "Activity log"],
];

test("every page in the menu has a clean address of its own", async ({ page }) => {
  await open(page, OWNER, "/devices");
  for (const [link, path, heading] of PAGES) {
    await nav(page).getByRole("link", { name: link }).click();
    await expect(page).toHaveURL(new RegExp(`${path}$`));
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    await expect(nav(page).getByRole("link", { name: link })).toHaveAttribute("aria-current", "page");
    expect(new URL(page.url()).hash).toBe("");
  }
});

test("refreshing a deep link stays there", async ({ page }) => {
  await open(page, OWNER, "/integrations/fake-home");
  await expect(page.getByRole("heading", { level: 1, name: "Fake Home" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Fake Home" })).toBeVisible();
  await expect(page).toHaveURL(/\/integrations\/fake-home$/);

  await page.goto("/automations/lights");
  await expect(page.getByRole("heading", { name: "lights.ts" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "lights.ts" })).toBeVisible();
});

test("back and forward walk the pages", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await nav(page).getByRole("link", { name: "Plugins" }).click();
  await expect(page).toHaveURL(/\/plugins$/);
  await nav(page).getByRole("link", { name: "People" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "People" })).toBeVisible();

  await page.goBack();
  await expect(page).toHaveURL(/\/plugins$/);
  await expect(page.getByRole("heading", { level: 1, name: "Plugins" })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { level: 1, name: "Your devices" })).toBeVisible();
  await page.goForward();
  await expect(page.getByRole("heading", { level: 1, name: "Plugins" })).toBeVisible();
});

test("back from an integration's page returns to the list", async ({ page }) => {
  await open(page, OWNER, "/integrations");
  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect(page).toHaveURL(/\/integrations\/fake-home$/);
  await page.goBack();
  await expect(page).toHaveURL(/\/integrations$/);
  await expect(page.getByRole("heading", { level: 1, name: "Integrations" })).toBeVisible();
});

test("old #page links get tidied into paths", async ({ page }) => {
  await open(page, OWNER, "/#users");
  await expect(page.getByRole("heading", { level: 1, name: "People" })).toBeVisible();
  await expect(page).toHaveURL(/\/users$/);
  expect(new URL(page.url()).hash).toBe("");

  await page.goto("/#automations/lights");
  await expect(page.getByRole("heading", { name: "lights.ts" })).toBeVisible();
  await expect(page).toHaveURL(/\/automations\/lights$/);
});

test("the bare address opens the page last seen, or Home the first time", async ({ page }) => {
  await open(page, OWNER, "/");
  await expect(page).toHaveURL(/\/home$/);
  await nav(page).getByRole("link", { name: "Plugins" }).click();
  await expect(page).toHaveURL(/\/plugins$/);
  await page.goto("/");
  await expect(page).toHaveURL(/\/plugins$/);
});

test("signing in returns to the address asked for", async ({ page }) => {
  await page.goto("/users");
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  await expect(page).toHaveURL(/\/users$/);
  await page.getByLabel("Name").fill(OWNER.username);
  await page.getByLabel("Password").fill(OWNER.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "People" })).toBeVisible();
  await expect(page).toHaveURL(/\/users$/);
});

test("an unknown address is a real 404, not a page", async ({ page }) => {
  const answer = await page.goto("/no-such-page");
  expect(answer?.status()).toBe(404);
  const deep = await page.goto("/no-such-page/at/all");
  expect(deep?.status()).toBe(404);
});

test("an API address that doesn't exist is refused, not served the page", async ({ page }) => {
  const signedOut = await page.request.get("/api/no-such-thing");
  expect(signedOut.status()).toBe(401);
  await open(page, OWNER, "/devices");
  const signedIn = await page.request.get("/api/no-such-thing");
  expect(signedIn.status()).toBe(404);
});

for (const [path, name] of [["/users", "People"], ["/audit", "Activity log"]]) {
  test(`a viewer who opens ${path} lands on Home`, async ({ page }) => {
    await open(page, person("viewer"), path);
    await expect(page).toHaveURL(/\/home$/);
    await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeVisible();
    await expect(page.getByRole("heading", { level: 1, name })).toHaveCount(0);
  });
}

test("a page anyone may open stays open for a viewer", async ({ page }) => {
  await open(page, person("viewer"), "/tokens");
  await expect(page.getByRole("heading", { level: 1, name: "API tokens" })).toBeVisible();
});

test("an integration that doesn't exist says so", async ({ page }) => {
  await open(page, OWNER, "/integrations/not-here");
  await expect(page.getByText("This integration isn't installed.")).toBeVisible();
  await page.getByRole("button", { name: "All integrations" }).click();
  await expect(page).toHaveURL(/\/integrations$/);
});

test("a script that doesn't exist says so", async ({ page }) => {
  await open(page, OWNER, "/automations/not-here");
  await expect(page.getByRole("alert").filter({ hasText: /not.*found|no such/i })).toBeVisible();
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page).toHaveURL(/\/automations$/);
});
