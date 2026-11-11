import { expect, type APIRequestContext, type Locator, type Page, request as playwrightRequest } from "@playwright/test";

export interface Person {
  username: string;
  password: string;
  role: "viewer" | "operator" | "editor" | "admin";
}

export const OWNER: Person = { username: "owner", password: "correct horse battery", role: "admin" };
export const person = (role: Person["role"], username: string = role): Person => ({ username, password: `${username} password long enough`, role });

export const FAKE_HOME = "./extensions/fake-home";

const BASE = "http://localhost:8099";

/** An API session outside any browser, for setting a spec's scene. What's being tested goes through the page. */
export async function ownerApi(): Promise<APIRequestContext> {
  const api = await playwrightRequest.newContext({ baseURL: BASE });
  const status = await (await api.get("/api/auth/status")).json();
  const response = await api.post(status.setupRequired ? "/api/auth/setup" : "/api/auth/login", { data: { username: OWNER.username, password: OWNER.password } });
  expect(response.ok(), await response.text()).toBe(true);
  return api;
}

async function ok(response: ReturnType<APIRequestContext["post"]>) {
  const answer = await response;
  expect(answer.ok(), await answer.text()).toBe(true);
  return answer;
}

export async function addPerson(api: APIRequestContext, who: Person) {
  await ok(api.post("/api/users", { data: { username: who.username, password: who.password, role: who.role } }));
}

/** Approves exactly what it asks for now: the service refuses an approval that doesn't say what was shown. */
export async function approve(api: APIRequestContext, source: string) {
  const extensions: { source: string; access: string[] }[] = await (await api.get("/api/extensions")).json();
  const access = extensions.find((e) => e.source === source)?.access;
  expect(access, `${source} isn't loaded`).toBeDefined();
  await ok(api.post("/api/extensions/approve", { data: { source, access } }));
}

/** The owner, every role, and fake-home approved and reporting. */
export async function peopleAndDevices(api: APIRequestContext) {
  for (const role of ["viewer", "operator", "editor"] as const) await addPerson(api, person(role));
  await approve(api, FAKE_HOME);
  await expect.poll(async () => (await (await api.get("/api/devices")).json()).length, { timeout: 30_000 }).toBeGreaterThan(0);
}

/** Signs in without the form, into the browser's own cookie jar. */
export async function signIn(page: Page, who: Person = OWNER) {
  const answer = await page.request.post("/api/auth/login", { data: { username: who.username, password: who.password } });
  expect(answer.ok(), await answer.text()).toBe(true);
}

export async function open(page: Page, who: Person, path: string) {
  await signIn(page, who);
  await page.goto(path);
}

export const nav = (page: Page) => page.getByRole("navigation", { name: "Main" });

/** The answer the browser's own session gets from the API. */
export function apiFromBrowser(page: Page, method: string, path: string, body?: unknown) {
  return page.evaluate(async ({ method, path, body }) => {
    const answer = await fetch(path, { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
    return answer.status;
  }, { method, path, body });
}

export const confirmDialog = (page: Page) => page.getByRole("alertdialog");
export const pressConfirm = (page: Page, action: string | RegExp) => confirmDialog(page).getByRole("button", { name: action }).click();

/** A card in a grid, by its title. */
export const card = (page: Page, title: string): Locator =>
  page.getByText(title, { exact: true }).first().locator('xpath=ancestor::div[contains(@class, "rounded-xl")][1]'); // a card has no role or name of its own

export const accountMenu = (page: Page, username: string) => page.getByRole("button", { name: `Account menu for ${username}` });

export async function signOut(page: Page, username: string) {
  await accountMenu(page, username).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
}

/** Replaces what's in the script editor. insertText, not typing, so Monaco doesn't add closing brackets of its own. */
export async function fillEditor(page: Page, text: string) {
  await page.locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(text);
}

/** Adds to the end of what's in the script editor. */
export async function appendToEditor(page: Page, text: string) {
  await page.locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Control+End");
  await page.keyboard.insertText(text);
}

/** Opens a role select and picks one. */
export async function chooseOption(page: Page, select: Locator, option: string | RegExp) {
  await select.click();
  await page.getByRole("option", { name: option }).click();
}

/** Approves each thing a script's card asks for. A script finds out what it needs as it runs, so more can appear after one. */
export async function approveEverything(scriptCard: Locator) {
  const asks = scriptCard.getByRole("button", { name: /^Approve: / });
  // Named once the engine has seen the device, about a second after it appears. Approving sooner fails the script until its next retry.
  await expect(scriptCard.getByRole("button", { name: /^Approve: .*unknown device/ })).toHaveCount(0);
  await expect(async () => {
    if (await asks.count()) await asks.first().click({ timeout: 2_000 });
    await expect(asks).toHaveCount(0, { timeout: 1_500 });
  }).toPass({ timeout: 30_000 });
}
