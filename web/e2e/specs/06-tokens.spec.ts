import { expect, test, type APIRequestContext } from "@playwright/test";
import { accountMenu, chooseOption, confirmDialog, open, OWNER, ownerApi, pressConfirm } from "../support/home";
import { freshHome } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async ({ context }) => {
  await freshHome({ config: "empty" });
  api = await ownerApi();
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
});

test("a token is shown once and can be copied", async ({ page }) => {
  await open(page, OWNER, "/devices");
  await accountMenu(page, OWNER.username).click();
  await page.getByRole("menuitem", { name: "API tokens" }).click();
  await expect(page).toHaveURL(/\/tokens$/);
  await expect(page.getByRole("heading", { name: "No tokens yet" })).toBeVisible();
  const create = page.getByRole("button", { name: "Create token" });
  await expect(create).toBeDisabled();

  await page.getByLabel("What is it for?").fill("Kitchen dashboard");
  await chooseOption(page, page.getByRole("combobox", { name: "Token role" }), /^Family/);
  await create.click();

  await expect(page.getByText('Copy the token for "Kitchen dashboard" now')).toBeVisible();
  await expect(page.getByText("This is the only time it is shown.")).toBeVisible();
  const secret = await page.getByTestId("token-secret").innerText();
  expect(secret).toMatch(/^hct_/);

  await page.getByRole("button", { name: "Copy", exact: true }).click();
  await expect(page.getByRole("button", { name: "Copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(secret);

  await page.getByRole("button", { name: "I've saved it" }).click();
  await expect(page.getByTestId("token-secret")).toHaveCount(0);
  const row = page.getByRole("row", { name: /Kitchen dashboard/ });
  await expect(row).toContainText("Family");
  await expect(row).toContainText("Never");
  await expect(row).toContainText(OWNER.username);

  // Never again, not on a reload and not from the API.
  await page.reload();
  await expect(page.getByRole("row", { name: /Kitchen dashboard/ })).toBeVisible();
  await expect(page.getByText(secret)).toHaveCount(0);
  expect(await (await api.get("/api/tokens")).text()).not.toContain(secret);
});

test("a token does what its role says, shows when it was used, and stops when deleted", async ({ page, playwright }) => {
  await open(page, OWNER, "/tokens");
  await page.getByLabel("What is it for?").fill("Read only");
  await page.getByRole("button", { name: "Create token" }).click();
  const secret = await page.getByTestId("token-secret").innerText();
  await page.getByRole("button", { name: "I've saved it" }).click();

  const program = await playwright.request.newContext({ baseURL: "http://localhost:8099", extraHTTPHeaders: { Authorization: `Bearer ${secret}` } });
  expect((await program.get("/api/devices")).ok()).toBe(true);
  expect((await program.get("/api/users")).status()).toBe(403);
  expect((await program.post("/api/automations/reload", { data: {} })).status()).toBe(403);
  // A password can't be changed with a token, however powerful.
  expect((await program.get("/api/tokens")).status()).toBe(403);

  await page.reload();
  await expect(page.getByRole("row", { name: /Read only/ })).toContainText(/Just now|min ago/);

  await page.getByRole("button", { name: "Delete Read only" }).click();
  await expect(confirmDialog(page)).toContainText("will stop working straight away");
  await pressConfirm(page, "Delete token");
  await expect(page.getByRole("heading", { name: "No tokens yet" })).toBeVisible();
  expect((await program.get("/api/devices")).status()).toBe(401);
  await program.dispose();
});
