import { expect, test } from "@playwright/test";
import { nav } from "../support/home";
import { freshHome, setupCode } from "../support/stack";

test.describe("first run", () => {
  test.beforeEach(async () => {
    await freshHome({ config: "empty" });
  });

  test("setup explains its password rules, and the button waits for them", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Welcome. Let's set up your home." })).toBeVisible();
    const create = page.getByRole("button", { name: "Create account and start" });
    await expect(create).toBeDisabled();
    await expect(page.getByText("no email recovery", { exact: false })).toBeVisible();
    await expect(page.getByLabel("Setup code")).toHaveCount(0); // on the computer running it, nothing to prove

    await page.getByLabel("Your name for signing in").fill("owner");
    const password = page.getByLabel("Password", { exact: true });
    await password.fill("short");
    await expect(page.getByText("Use at least 12 characters (5 so far).")).toBeVisible();
    await expect(password).toHaveAttribute("aria-invalid", "true");
    await expect(create).toBeDisabled();

    await password.fill("correct horse battery");
    await expect(page.getByText("At least 12 characters.", { exact: false })).toBeVisible();
    const again = page.getByLabel("Type the password again");
    await again.fill("something else entirely");
    await expect(page.getByText("These two don't match yet.")).toBeVisible();
    await expect(create).toBeDisabled();

    await again.fill("correct horse battery");
    await expect(create).toBeEnabled();
  });

  test("the service refuses a name it can't use, and says why", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Your name for signing in").fill("has a space");
    await page.getByLabel("Password", { exact: true }).fill("correct horse battery");
    await page.getByLabel("Type the password again").fill("correct horse battery");
    await page.getByRole("button", { name: "Create account and start" }).click();
    await expect(page.getByRole("alert").filter({ hasText: "the username must be 1 to 64 letters" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Welcome. Let's set up your home." })).toBeVisible();
  });

  test("the first account is an administrator and gets the welcome once", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Your name for signing in").fill("owner");
    await page.getByLabel("Password", { exact: true }).fill("correct horse battery");
    await page.getByLabel("Type the password again").fill("correct horse battery");
    await page.getByRole("button", { name: "Create account and start" }).click();

    const welcome = page.getByRole("dialog", { name: "You're all set" });
    await expect(welcome).toBeVisible();
    await welcome.getByRole("button", { name: "See my devices" }).click();
    await expect(welcome).toBeHidden();
    await expect(page).toHaveURL(/\/devices$/);
    await expect(page.getByRole("heading", { name: "Your devices" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "No devices yet" })).toBeVisible();
    await expect(nav(page).getByRole("link", { name: "People" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Account menu for owner" })).toContainText("Administrator");

    await page.reload();
    await expect(page.getByRole("heading", { name: "Your devices" })).toBeVisible();
    await expect(welcome).toBeHidden();
  });

  test("the welcome can lead to adding people", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Your name for signing in").fill("owner");
    await page.getByLabel("Password", { exact: true }).fill("correct horse battery");
    await page.getByLabel("Type the password again").fill("correct horse battery");
    await page.getByRole("button", { name: "Create account and start" }).click();
    await page.getByRole("dialog", { name: "You're all set" }).getByRole("button", { name: "Add people" }).click();
    await expect(page).toHaveURL(/\/users$/);
    await expect(page.getByRole("heading", { name: "People" })).toBeVisible();
  });

  test("setup can't run twice", async ({ page, request }) => {
    await page.goto("/");
    await page.getByLabel("Your name for signing in").fill("owner");
    await page.getByLabel("Password", { exact: true }).fill("correct horse battery");
    await page.getByLabel("Type the password again").fill("correct horse battery");
    await page.getByRole("button", { name: "Create account and start" }).click();
    await expect(page.getByRole("dialog", { name: "You're all set" })).toBeVisible();

    const second = await request.post("/api/auth/setup", { data: { username: "intruder", password: "another long password" } });
    expect(second.status()).toBe(403);
  });

  test("from any other address, setup needs the one-time code from the service's logs", async ({ page }) => {
    await page.goto("http://10.76.1.10:8099/");
    await page.getByLabel("Your name for signing in").fill("owner");
    await page.getByLabel("Password", { exact: true }).fill("correct horse battery");
    await page.getByLabel("Type the password again").fill("correct horse battery");
    const create = page.getByRole("button", { name: "Create account and start" });
    const code = page.getByLabel("Setup code");
    await expect(code).toBeVisible();
    await expect(create).toBeDisabled();

    await code.fill("not-the-code");
    await create.click();
    await expect(page.getByRole("alert").filter({ hasText: "That setup code isn't right" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Welcome. Let's set up your home." })).toBeVisible();

    await expect.poll(setupCode).not.toBe("");
    await code.fill(await setupCode());
    await create.click();
    await expect(page.getByRole("dialog", { name: "You're all set" })).toBeVisible();
  });

  test("through a proxy the code is asked for even on localhost", async ({ request }) => {
    const direct = await (await request.get("/api/auth/status")).json();
    expect(direct.setupRequired).toBe(true);
    expect(direct.setupTokenRequired).toBeFalsy();
    const proxied = await (await request.get("/api/auth/status", { headers: { "X-Forwarded-For": "203.0.113.9" } })).json();
    expect(proxied.setupTokenRequired).toBe(true);
    const refused = await request.post("/api/auth/setup", { headers: { "X-Forwarded-For": "203.0.113.9" }, data: { username: "intruder", password: "another long password" } });
    expect(refused.status()).toBe(403);
    expect((await (await request.get("/api/auth/status")).json()).setupRequired).toBe(true);
  });
});
