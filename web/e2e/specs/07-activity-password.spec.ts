import { expect, test } from "@playwright/test";
import { accountMenu, nav, open, OWNER, ownerApi, person, addPerson } from "../support/home";
import { freshHome } from "../support/stack";

test.beforeEach(async () => {
  await freshHome({ config: "empty" });
  await ownerApi();
});

test("the activity log says who did what, newest first", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await nav(page).getByRole("link", { name: "People" }).click();
  await page.getByRole("button", { name: "Add a person" }).click();
  const dialog = page.getByRole("dialog", { name: "Add a person" });
  await dialog.getByLabel("Name").fill("logged-person");
  await dialog.getByLabel("Temporary password").fill("a long enough password");
  await dialog.getByRole("button", { name: "Add person" }).click();
  await expect(dialog).toBeHidden();

  await nav(page).getByRole("link", { name: "Activity log" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Activity log" })).toBeVisible();
  await expect(page.getByRole("columnheader", { name: "Who" })).toBeVisible();
  const rows = page.getByRole("row");
  await expect(rows.nth(1)).toContainText("logged-person");
  await expect(page.getByRole("row").filter({ hasText: "login.success" }).first()).toContainText(OWNER.username);
  await expect(page.getByRole("row").filter({ hasText: "setup" }).first()).toContainText("first admin created");

  await page.request.post("/api/auth/login", { data: { username: "nobody", password: "wrong wrong wrong" } });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(rows.nth(1)).toContainText("login.failure");
  await expect(rows.nth(1)).toContainText("nobody");
});

test("the log never holds a password", async ({ page }) => {
  await open(page, OWNER, "/audit");
  await page.request.post("/api/auth/login", { data: { username: OWNER.username, password: "this wrong password is secret" } });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByRole("row").filter({ hasText: "login.failure" })).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText("this wrong password");
  await expect(page.getByRole("main")).not.toContainText(OWNER.password);
});

test("changing your password checks the old one and replaces it", async ({ page, browser }) => {
  await open(page, OWNER, "/devices");
  await accountMenu(page, OWNER.username).click();
  await page.getByRole("menuitem", { name: "Change password" }).click();
  const dialog = page.getByRole("dialog", { name: "Change your password" });
  const change = dialog.getByRole("button", { name: "Change password" });
  await expect(change).toBeDisabled();

  await dialog.getByLabel("Current password").fill("this one is wrong");
  await dialog.getByLabel("New password", { exact: true }).fill("short");
  await expect(dialog.getByText("7 more characters needed.")).toBeVisible();
  await dialog.getByLabel("New password", { exact: true }).fill("my new long password");
  await dialog.getByLabel("New password again").fill("not the same thing");
  await expect(dialog.getByText("These two don't match yet.")).toBeVisible();
  await expect(change).toBeDisabled();

  await dialog.getByLabel("New password again").fill("my new long password");
  await change.click();
  await expect(dialog.getByRole("alert")).toHaveText("Current password is wrong");

  await dialog.getByLabel("Current password").fill(OWNER.password);
  await change.click();
  await expect(page.getByText("Password changed")).toBeVisible();
  await expect(dialog).toBeHidden();

  const fresh = await browser.newContext({ baseURL: "http://localhost:8099" });
  expect((await fresh.request.post("/api/auth/login", { data: { username: OWNER.username, password: OWNER.password } })).status()).toBe(401);
  expect((await fresh.request.post("/api/auth/login", { data: { username: OWNER.username, password: "my new long password" } })).ok()).toBe(true);
  await fresh.close();
});

test("anyone can change their own password, and Cancel changes nothing", async ({ page }) => {
  const viewer = person("viewer");
  const api = await ownerApi();
  await addPerson(api, viewer);
  await open(page, viewer, "/devices");
  await accountMenu(page, viewer.username).click();
  await page.getByRole("menuitem", { name: "Change password" }).click();
  const dialog = page.getByRole("dialog", { name: "Change your password" });
  await dialog.getByLabel("Current password").fill(viewer.password);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  await accountMenu(page, viewer.username).click();
  await page.getByRole("menuitem", { name: "Change password" }).click();
  await expect(dialog.getByLabel("Current password")).toHaveValue("");
});

for (const [what, keep, status] of [["deletes API tokens by default", false, 401], ["can leave them working", true, 200]] as const) {
  test(`changing your password ${what}`, async ({ page, playwright }) => {
    await open(page, OWNER, "/devices");
    const token = await (await page.request.post("/api/tokens", { data: { name: "program", role: "viewer" } })).json();
    await accountMenu(page, OWNER.username).click();
    await page.getByRole("menuitem", { name: "Change password" }).click();
    const dialog = page.getByRole("dialog", { name: "Change your password" });
    const drop = dialog.getByRole("checkbox", { name: /Also delete my API tokens/ });
    await expect(drop).toBeChecked();
    if (keep) await drop.uncheck();
    await dialog.getByLabel("Current password").fill(OWNER.password);
    await dialog.getByLabel("New password", { exact: true }).fill("my new long password");
    await dialog.getByLabel("New password again").fill("my new long password");
    await dialog.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("Password changed")).toBeVisible();

    const program = await playwright.request.newContext({ baseURL: "http://localhost:8099", extraHTTPHeaders: { Authorization: `Bearer ${token.token}` } });
    expect((await program.get("/api/devices")).status()).toBe(status);
    await program.dispose();
    // This browser stays signed in.
    await expect(page.getByRole("heading", { name: "Your devices" })).toBeVisible();
    expect((await page.request.get("/api/devices")).ok()).toBe(true);
  });
}
