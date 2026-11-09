import { expect, test, type APIRequestContext } from "@playwright/test";
import { addPerson, appendToEditor, nav, OWNER, ownerApi, person, signOut } from "../support/home";
import { freshHome } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome();
  api = await ownerApi();
});

async function fillForm(page: import("@playwright/test").Page, username: string, password: string) {
  await page.getByLabel("Name").fill(username);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

test("signing in opens the home", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  await expect(page.getByText("an administrator can reset it")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeDisabled();
  await fillForm(page, OWNER.username, OWNER.password);
  await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeVisible();
  await expect(nav(page).getByRole("link", { name: "Home" })).toHaveAttribute("aria-current", "page");
});

test("a wrong password, or an unknown name, says the same thing and stays on the form", async ({ page }) => {
  await page.goto("/");
  await fillForm(page, OWNER.username, "not the password at all");
  await expect(page.getByRole("alert")).toHaveText("That name or password isn't right. Check both and try again.");
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();

  await fillForm(page, "nobody-here", "not the password at all");
  await expect(page.getByRole("alert")).toHaveText("That name or password isn't right. Check both and try again.");
});

test("too many wrong passwords lock the name out, even with the right one", async ({ page }) => {
  await page.goto("/");
  for (let attempt = 0; attempt < 5; attempt++) {
    await fillForm(page, OWNER.username, `wrong password number ${attempt}`);
    await expect(page.getByRole("alert")).toHaveText("That name or password isn't right. Check both and try again.");
    await page.getByRole("button", { name: "Sign in" }).waitFor();
  }
  await fillForm(page, OWNER.username, OWNER.password);
  await expect(page.getByRole("alert")).toHaveText("Too many attempts, try again shortly");
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
});

test("signing out ends the session for real", async ({ page, context }) => {
  await page.goto("/");
  await fillForm(page, OWNER.username, OWNER.password);
  await expect(page.getByRole("heading", { level: 1, name: "Home" })).toBeVisible();
  const before = await context.cookies();
  expect(before.map((c) => c.name)).toContain("hc_session");

  await signOut(page, OWNER.username);
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  await expect(page.getByText("You're signed out")).toBeVisible();

  await page.reload();
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  const replay = await page.request.get("/api/devices", { headers: { Cookie: before.map((c) => `${c.name}=${c.value}`).join("; ") } });
  expect(replay.status()).toBe(401);
});

test("a session that ends keeps the page and an open script under the sign-in overlay", async ({ page, browser }) => {
  const second = person("admin", "second-admin");
  await addPerson(api, second);
  const owner = (await (await api.get("/api/users")).json()).find((u: { username: string }) => u.username === OWNER.username);

  await page.goto("/");
  await fillForm(page, OWNER.username, OWNER.password);
  await nav(page).getByRole("link", { name: "Automations" }).click();
  await page.getByRole("button", { name: "Edit lights" }).click();
  await expect(page).toHaveURL(/\/automations\/lights$/);
  await appendToEditor(page, "\n//not_saved_yet");
  await expect(page.getByText("Unsaved changes")).toBeVisible();

  // Another administrator gives this account a new password, which ends every session it has.
  const other = await (await browser.newContext({ baseURL: "http://localhost:8099" })).request;
  expect((await other.post("/api/auth/login", { data: { username: second.username, password: second.password } })).ok()).toBe(true);
  const newPassword = "a brand new password";
  expect((await other.patch(`/api/users/${owner.id}`, { data: { password: newPassword } })).ok()).toBe(true);

  await expect(page.getByRole("heading", { name: "Sign in again to continue" })).toBeVisible();
  await expect(page.getByText("nothing you typed is lost")).toBeVisible();
  // What's underneath is out of reach, for a mouse and for a screen reader.
  await expect(page.getByRole("main")).toHaveCount(1);
  await expect(page.getByRole("navigation", { name: "Main" })).toHaveCount(0);

  await fillForm(page, OWNER.username, OWNER.password);
  await expect(page.getByText("That name or password isn't right. Check both and try again.")).toBeVisible();

  await page.getByLabel("Password").fill(newPassword);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Sign in again to continue" })).toBeHidden();
  await expect(page).toHaveURL(/\/automations\/lights$/);
  await expect(page.getByText("Unsaved changes")).toBeVisible();
  await expect(page.locator(".monaco-editor .view-lines")).toContainText("//not_saved_yet");
});

test("when the service can't be reached the sign-in page says so and can try again", async ({ page }) => {
  await page.route("**/api/auth/status", (route) => route.abort());
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Can't reach Home Control" })).toBeVisible();
  await page.unroute("**/api/auth/status");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
});
