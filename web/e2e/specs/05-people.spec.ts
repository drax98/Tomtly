import { expect, test, type APIRequestContext } from "@playwright/test";
import { addPerson, chooseOption, confirmDialog, nav, open, OWNER, ownerApi, person, pressConfirm, signOut } from "../support/home";
import { freshHome } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "empty" });
  api = await ownerApi();
});

const row = (page: import("@playwright/test").Page, name: string) => page.getByRole("row").filter({ has: page.getByRole("cell", { name, exact: false }) });

test("adding a person: the form guides, the name works straight away", async ({ page, browser }) => {
  await open(page, OWNER, "/users");
  await page.getByRole("button", { name: "Add a person" }).click();
  const dialog = page.getByRole("dialog", { name: "Add a person" });
  const add = dialog.getByRole("button", { name: "Add person" });
  await expect(add).toBeDisabled();
  await dialog.getByLabel("Name").fill("grandma");
  await dialog.getByLabel("Temporary password").fill("short");
  await expect(add).toBeDisabled();
  await dialog.getByLabel("Temporary password").fill("a long enough password");
  await chooseOption(page, dialog.getByRole("combobox", { name: "Role" }), /^Viewer/);
  await expect(dialog.getByRole("paragraph").filter({ hasText: "Can look at everything, but can't change anything." })).toBeVisible();
  await add.click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText("grandma can now sign in")).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Role of grandma" })).toHaveText("Viewer");

  const other = await (await browser.newContext()).newPage();
  await other.goto("/");
  await other.getByLabel("Name").fill("grandma");
  await other.getByLabel("Password").fill("a long enough password");
  await other.getByRole("button", { name: "Sign in" }).click();
  await expect(other.getByRole("button", { name: "Account menu for grandma" })).toContainText("Viewer");
  await other.close();
});

test("a name already taken, or one that isn't allowed, is refused in the dialog", async ({ page }) => {
  await addPerson(api, person("viewer", "taken"));
  await open(page, OWNER, "/users");
  await page.getByRole("button", { name: "Add a person" }).click();
  const dialog = page.getByRole("dialog", { name: "Add a person" });
  await dialog.getByLabel("Name").fill("taken");
  await dialog.getByLabel("Temporary password").fill("a long enough password");
  await dialog.getByRole("button", { name: "Add person" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("that username is taken");
  await dialog.getByLabel("Name").fill("not allowed!");
  await dialog.getByRole("button", { name: "Add person" }).click();
  await expect(dialog.getByRole("alert")).toContainText("the username must be 1 to 64 letters");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
});

test("changing someone's role shows at once", async ({ page }) => {
  await addPerson(api, person("viewer", "kid"));
  await open(page, OWNER, "/users");
  await chooseOption(page, page.getByRole("combobox", { name: "Role of kid" }), /^Family/);
  await expect(page.getByRole("combobox", { name: "Role of kid" })).toHaveText("Family");
  await page.reload();
  await expect(page.getByRole("combobox", { name: "Role of kid" })).toHaveText("Family");
});

test("pausing stops sign-in and ends the session; allowing brings it back", async ({ page, browser }) => {
  const kid = person("operator", "kid");
  await addPerson(api, kid);
  const theirs = await (await browser.newContext()).newPage();
  await open(theirs, kid, "/devices");
  await expect(theirs.getByRole("heading", { name: "Your devices" })).toBeVisible();

  await open(page, OWNER, "/users");
  await row(page, "kid").getByRole("button", { name: "Pause" }).click();
  await expect(row(page, "kid").getByText("Paused")).toBeVisible();

  await expect(theirs.getByRole("heading", { name: "Sign in again to continue" })).toBeVisible();
  await theirs.getByLabel("Name").fill(kid.username);
  await theirs.getByLabel("Password").fill(kid.password);
  await theirs.getByRole("button", { name: "Sign in" }).click();
  await expect(theirs.getByRole("alert")).toHaveText("That name or password isn't right. Check both and try again.");

  await row(page, "kid").getByRole("button", { name: "Allow sign-in" }).click();
  await expect(row(page, "kid").getByText("Paused")).toBeHidden();
  await theirs.getByRole("button", { name: "Sign in" }).click();
  await expect(theirs.getByRole("heading", { name: "Your devices" })).toBeVisible();
  await theirs.close();
});

test("setting someone's password replaces the old one", async ({ page, browser }) => {
  const kid = person("viewer", "kid");
  await addPerson(api, kid);
  await open(page, OWNER, "/users");
  await row(page, "kid").getByRole("button", { name: "Set password" }).click();
  const dialog = page.getByRole("dialog", { name: "New password for kid" });
  await expect(dialog.getByText("Tell them the new password in person.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Set password" })).toBeDisabled();
  await dialog.getByLabel("New password").fill("kid's new long password");
  await dialog.getByRole("button", { name: "Set password" }).click();
  await expect(page.getByText("New password set for kid")).toBeVisible();

  const theirs = await browser.newContext({ baseURL: "http://localhost:8099" });
  expect((await theirs.request.post("/api/auth/login", { data: { username: "kid", password: kid.password } })).status()).toBe(401);
  expect((await theirs.request.post("/api/auth/login", { data: { username: "kid", password: "kid's new long password" } })).ok()).toBe(true);
  await theirs.close();
});

test("removing someone asks first, and their sign-in and tokens stop", async ({ page, browser }) => {
  const kid = person("viewer", "kid");
  await addPerson(api, kid);
  const theirs = await browser.newContext({ baseURL: "http://localhost:8099" });
  await theirs.request.post("/api/auth/login", { data: { username: "kid", password: kid.password } });
  const token = await (await theirs.request.post("/api/tokens", { data: { name: "kid's", role: "viewer" } })).json();

  await open(page, OWNER, "/users");
  await page.getByRole("button", { name: "Remove kid" }).click();
  await expect(confirmDialog(page)).toContainText("Their API tokens stop working too.");
  await confirmDialog(page).getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("combobox", { name: "Role of kid" })).toBeVisible();

  await page.getByRole("button", { name: "Remove kid" }).click();
  await pressConfirm(page, "Remove");
  await expect(page.getByRole("combobox", { name: "Role of kid" })).toHaveCount(0);

  expect((await theirs.request.get("/api/devices")).status()).toBe(401);
  const withToken = await page.request.get("/api/devices", { headers: { Authorization: `Bearer ${token.token}`, Cookie: "" } });
  expect(withToken.status()).toBe(401);
  await theirs.close();
});

test("the last administrator can't be paused, demoted or removed", async ({ page }) => {
  await open(page, OWNER, "/users");
  const mine = row(page, OWNER.username);
  await expect(mine.getByText("You")).toBeVisible();

  await mine.getByRole("button", { name: "Pause" }).click();
  await expect(confirmDialog(page)).toContainText("Changing it signs you out everywhere");
  await pressConfirm(page, "Continue");
  await expect(page.getByRole("alert").filter({ hasText: "there must stay at least one enabled admin" })).toBeVisible();

  await page.getByRole("button", { name: `Remove ${OWNER.username}` }).click();
  await expect(confirmDialog(page)).toContainText("This is your own account");
  await pressConfirm(page, "Remove");
  await expect(page.getByRole("alert").filter({ hasText: "there must stay at least one enabled admin" })).toBeVisible();

  await chooseOption(page, page.getByRole("combobox", { name: `Role of ${OWNER.username}` }), /^Viewer/);
  await pressConfirm(page, "Continue");
  await expect(page.getByRole("alert").filter({ hasText: "there must stay at least one enabled admin" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: `Role of ${OWNER.username}` })).toHaveText("Administrator");
});

test("changing your own account warns, and signs you out when it goes through", async ({ page }) => {
  await addPerson(api, person("admin", "second-admin"));
  await open(page, OWNER, "/users");
  await row(page, OWNER.username).getByRole("button", { name: "Set password" }).click();
  const dialog = page.getByRole("dialog", { name: `New password for ${OWNER.username}` });
  await expect(dialog.getByText("signs you out everywhere")).toBeVisible();
  await dialog.getByLabel("New password").fill("my own new long password");
  await dialog.getByRole("button", { name: "Set password" }).click();
  await expect(page.getByRole("heading", { name: "Sign in again to continue" })).toBeVisible();
});

test("signing out from the menu works after managing people", async ({ page }) => {
  await open(page, OWNER, "/users");
  await signOut(page, OWNER.username);
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
  await expect(nav(page)).toHaveCount(0);
});
