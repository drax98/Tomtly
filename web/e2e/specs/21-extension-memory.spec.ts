import { expect, test, type APIRequestContext } from "@playwright/test";
import { approve, card, nav, open, OWNER, ownerApi, peopleAndDevices } from "../support/home";
import { deviceValue, freshHome } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async () => {
  await freshHome({ config: "memory" });
  api = await ownerApi();
  await peopleAndDevices(api);
  await approve(api, "./extensions/memory-bomb");
});

test("an extension that uses too much memory is stopped, the page says so, and the rest of the home keeps working", async ({ page }) => {
  await open(page, OWNER, "/plugins");
  const bomb = card(page, "Memory Bomb");
  await expect(bomb.getByText("Waiting for your OK")).toBeVisible({ timeout: 60_000 });
  await expect(bomb.getByText(/Stopped for using too much memory \(the service's heap passed 256 MB while it was running\)/)).toBeVisible();
  await expect(bomb.getByText("Running", { exact: true })).toHaveCount(0);

  // The other extension is untouched and still reports its devices.
  await nav(page).getByRole("link", { name: "Integrations" }).click();
  await expect(card(page, "Fake Home").getByText("Running", { exact: true })).toBeVisible();
  await expect.poll(async () => (await (await api.get("/api/devices")).json()).length).toBeGreaterThan(0);

  await nav(page).getByRole("link", { name: "Devices" }).click();
  const power = page.getByRole("switch", { name: "Desk lamp Power" });
  await expect(power).toBeVisible();
  await power.click();
  await expect(power).toBeChecked();
  await expect.poll(() => deviceValue("lamp", "switch")).toBe(true);
});
