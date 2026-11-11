import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { approve, chooseOption, FAKE_HOME, open, OWNER, ownerApi, peopleAndDevices } from "../support/home";
import { changeDevice, deviceState, deviceValue, freshHome, startService, stopService } from "../support/stack";

let api: APIRequestContext;

test.beforeEach(async ({ page }) => {
  await freshHome();
  api = await ownerApi();
  await peopleAndDevices(api);
  await open(page, OWNER, "/devices");
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
});

const section = (page: Page, name: string) => page.getByRole("region", { name });

test("the header says how many answer, and rooms group the cards", async ({ page }) => {
  await expect(page.getByRole("status").filter({ hasText: "5 of 6" })).toBeVisible();
  for (const room of ["Living room", "Bedroom", "Other"]) await expect(page.getByRole("heading", { level: 2, name: room })).toBeVisible();
  await expect(section(page, "Living room").getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
  await expect(section(page, "Bedroom").getByRole("slider", { name: "Bedroom blinds position" })).toBeVisible();
  await expect(section(page, "Other").getByText("Front door")).toBeVisible();
  await expect(section(page, "Living room").getByText("1 of 2 on")).toBeVisible();

  const jump = page.getByRole("navigation", { name: "Jump to" });
  await expect(jump.getByRole("link", { name: "Living room" })).toBeVisible();
  await expect(jump.getByRole("link", { name: "Bedroom" })).toBeVisible();
  await jump.getByRole("link", { name: "Other" }).click();
  await expect(section(page, "Other")).toBeInViewport();
});

test("rooms appear in the order config.json lists them", async ({ page }) => {
  await expect(page.getByRole("navigation", { name: "Jump to" }).getByRole("link")).toHaveText(["Living room", "Bedroom", "Other"]);
});

test("grouping by type is remembered", async ({ page }) => {
  await page.getByRole("tab", { name: "By type" }).click();
  for (const kind of ["Climate", "Lights", "Blinds", "Cleaning", "Sensors"]) await expect(page.getByRole("heading", { level: 2, name: kind })).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: "Living room" })).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("tab", { name: "By type" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { level: 2, name: "Lights" })).toBeVisible();
});

test("finding a device by name, room or type, and the empty result", async ({ page }) => {
  const find = page.getByRole("searchbox", { name: "Find a device" });
  await find.fill("bedroom");
  await expect(page.getByRole("slider", { name: "Bedroom blinds position" })).toBeVisible();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toHaveCount(0);
  await find.fill("lights");
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
  await find.fill("no such thing");
  await expect(page.getByRole("heading", { name: 'Nothing matches "no such thing"' })).toBeVisible();
  await page.getByRole("button", { name: "Clear the search" }).click();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
});

test("Boolean: a switch reaches the device, and the device's own change reaches the page", async ({ page }) => {
  const power = page.getByRole("switch", { name: "Desk lamp Power" });
  await expect(power).not.toBeChecked();
  await power.click();
  await expect(power).toBeChecked();
  await expect.poll(() => deviceValue("lamp", "switch")).toBe(true);
  expect((await deviceState()).commands).toContainEqual({ id: "lamp", code: "switch", value: true });
  await changeDevice({ id: "lamp", code: "switch", value: false });
  await expect(power).not.toBeChecked();
});

test("Integer: a slider sends the new value and shows it with its unit", async ({ page }) => {
  const brightness = page.getByRole("slider", { name: "Desk lamp Brightness" });
  await expect(brightness).toHaveAttribute("aria-valuenow", "40");
  await brightness.focus();
  await page.keyboard.press("ArrowRight");
  await expect.poll(() => deviceValue("lamp", "bright_value")).toBe(50);
  await expect(page.getByRole("slider", { name: "Desk lamp Brightness" })).toHaveAttribute("aria-valuenow", "50");
  await expect(page.getByText("50 %")).toBeVisible();
});

test("Enum: a choice reaches the device", async ({ page }) => {
  const scene = page.getByRole("combobox", { name: "Desk lamp Scene" });
  await expect(scene).toHaveText("Reading");
  await chooseOption(page, scene, "Night");
  await expect.poll(() => deviceValue("lamp", "scene")).toBe("night");
  await expect(page.getByRole("combobox", { name: "Desk lamp Scene" })).toHaveText("Night");
});

test("Action: a button says Sent and the device gets the command", async ({ page }) => {
  const flash = page.getByRole("button", { name: "Flash" });
  await flash.click();
  await expect(flash).toContainText("Sent");
  await expect.poll(async () => (await deviceState()).commands).toContainEqual({ id: "lamp", code: "flash", value: "flash" });
  await expect(flash).toHaveText("Flash");
});

test("read-only values are shown and can't be changed; rare ones wait in the drawer", async ({ page }) => {
  await expect(page.getByText("Firmware", { exact: true })).toBeVisible();
  await expect(page.getByText("1.2.3", { exact: true })).toBeVisible();
  await expect(page.getByText("Power use now", { exact: true })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Desk lamp Light mode" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Advanced settings" })).toHaveCount(0);

  await page.getByRole("button", { name: "Details of Desk lamp" }).click();
  const drawer = page.getByRole("dialog", { name: "Desk lamp" });
  await expect(drawer.getByText("work_mode", { exact: true })).toBeVisible();
  await chooseOption(page, drawer.getByRole("combobox", { name: "Desk lamp Light mode" }), "Colour");
  await expect.poll(() => deviceValue("lamp", "work_mode")).toBe("colour");
});

test("climate card: temperature, target, mode and power", async ({ page }) => {
  await expect(page.getByText("20.5 °C", { exact: true })).toBeVisible();
  await expect(page.getByText("Heating to 21.0 °C")).toBeVisible();
  await page.getByRole("button", { name: "Raise Hall thermostat" }).click();
  await expect.poll(() => deviceValue("thermostat", "temp_set")).toBe(215);
  await expect(page.getByText("Heating to 21.5 °C")).toBeVisible();
  await page.getByRole("button", { name: "Lower Hall thermostat" }).click();
  await expect.poll(() => deviceValue("thermostat", "temp_set")).toBe(210);
  await chooseOption(page, page.getByRole("combobox", { name: "Hall thermostat Mode" }), "Manual");
  await expect.poll(() => deviceValue("thermostat", "mode")).toBe("manual");

  await page.getByRole("switch", { name: "Hall thermostat Power" }).click();
  await expect.poll(() => deviceValue("thermostat", "switch")).toBe(false);
  await expect(page.getByText("Turn it on to change the target.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Raise Hall thermostat" })).toBeDisabled();
});

test("cover card: buttons and position", async ({ page }) => {
  await expect(page.getByText("30% open")).toBeVisible();
  await page.getByRole("button", { name: "Open", exact: true }).click();
  await expect.poll(() => deviceValue("blinds", "percent_state")).toBe(100);
  await expect(page.getByRole("slider", { name: "Bedroom blinds position" })).toHaveAttribute("aria-valuenow", "100");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect.poll(() => deviceValue("blinds", "percent_state")).toBe(0);
  const position = page.getByRole("slider", { name: "Bedroom blinds position" });
  await expect(position).toHaveAttribute("aria-valuenow", "0");
  await position.focus();
  await page.keyboard.press("ArrowRight");
  await expect.poll(() => deviceValue("blinds", "percent_control")).toBe(10);
  await expect(page.getByText("10% open")).toBeVisible();
});

test("vacuum card: commands change its state", async ({ page }) => {
  await expect(page.getByText("Docked", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Start cleaning" }).click();
  await expect.poll(() => deviceValue("vacuum", "state")).toBe("cleaning");
  await expect(page.getByText("Cleaning", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Go to dock" }).click();
  await expect(page.getByText("Docked", { exact: true })).toBeVisible();
  await chooseOption(page, page.getByRole("combobox", { name: "Robot vacuum Fan speed" }), "Turbo");
  await expect.poll(() => deviceValue("vacuum", "fan_speed")).toBe("turbo");
});

test("sensor card: open or closed, with a low battery called out", async ({ page }) => {
  await expect(page.getByText("Low, 15%")).toBeVisible();
  await expect(page.getByRole("progressbar", { name: "Battery level" }).first()).toBeVisible();
  await changeDevice({ id: "door", code: "open", value: false });
  await expect(section(page, "Other").getByText("Closed", { exact: true }).first()).toBeVisible();
});

test("an offline device says so, and comes back by itself", async ({ page }) => {
  await expect(page.getByText("1 device is offline.")).toBeVisible();
  await expect(page.getByText("Offline. Check it has power and Wi-Fi.")).toHaveCount(1);
  await changeDevice({ id: "lamp", online: false });
  await expect(page.getByText("2 devices are offline.")).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "4 of 6" })).toBeVisible();
  await changeDevice({ id: "lamp", online: true });
  await expect(page.getByText("1 device is offline.")).toBeVisible();
});

test("a device that refuses a command: the switch goes back", async ({ page }) => {
  await changeDevice({ refuse: true });
  const power = page.getByRole("switch", { name: "Desk lamp Power" });
  await power.click();
  await expect(power).toBeChecked(); // shown at once, until the device has had its say
  await expect(power).not.toBeChecked({ timeout: 20_000 });
  expect(await deviceValue("lamp", "switch")).toBe(false);
});

test("a device that refuses a command says why", async ({ page }) => {
  await changeDevice({ refuse: true });
  await page.getByRole("switch", { name: "Desk lamp Power" }).click();
  await expect(page.getByText(/the device refused that/)).toBeVisible();
});

test("a command the service itself refuses shows at once", async ({ page }) => {
  // The page's view of the devices stays as it was, so the switch is still there to press after the device has gone.
  const stale = await (await api.get("/api/devices")).json();
  await page.route("**/api/devices", (route) => route.fulfill({ json: stale }));
  await api.delete("/api/devices/fake-home:lamp");
  const power = page.getByRole("switch", { name: "Desk lamp Power" });
  await power.click();
  await expect(page.getByText("That didn't go through")).toBeVisible();
  await expect(power).not.toBeChecked();
});

test("when the service goes away the page says so and recovers without a reload", async ({ page }) => {
  await stopService("tomtly");
  await expect(page.getByRole("status").filter({ hasText: "Can't reach the home" })).toBeVisible();
  await expect(page.getByRole("alert").filter({ hasText: "Can't reach the home service" })).toBeVisible();
  await startService("tomtly");
  await expect(page.getByRole("status").filter({ hasText: "5 of 6" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("alert").filter({ hasText: "Can't reach the home service" })).toBeHidden();
});

test("with no devices the page says what to do", async ({ page }) => {
  await freshHome({ config: "empty" });
  await ownerApi();
  await open(page, OWNER, "/devices");
  await expect(page.getByRole("heading", { name: "No devices yet" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Check again" })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "0 of 0" })).toBeVisible();
});

test("until it's approved an integration's devices don't appear", async ({ page }) => {
  await freshHome();
  const owner = await ownerApi();
  await open(page, OWNER, "/devices");
  await expect(page.getByRole("heading", { name: "No devices yet" })).toBeVisible();
  await approve(owner, FAKE_HOME);
  await page.getByRole("button", { name: "Check again" }).click();
  await expect(page.getByRole("switch", { name: "Desk lamp Power" })).toBeVisible();
});

test("a card opens a drawer with everything about the device, and a pin stays on the card for everyone", async ({ page, browser }) => {
  const lampCard = section(page, "Living room").locator(".rounded-xl").filter({ has: page.getByRole("button", { name: "Details of Desk lamp" }) });
  await lampCard.click({ position: { x: 36, y: 40 } }); // its icon, not a control
  const drawer = page.getByRole("dialog", { name: "Desk lamp" });
  await expect(drawer.getByText("Fake lamp").first()).toBeVisible();
  await expect(drawer.getByText("Living room")).toBeVisible();
  await expect(drawer.getByText(/lamp$/).first()).toBeVisible(); // its id
  for (const code of ["bright_value", "scene", "firmware", "cur_power"]) await expect(drawer.getByText(code, { exact: true })).toBeVisible();

  const pin = drawer.getByRole("button", { name: /^Pin .* to the card$/ }).first();
  const name = (await pin.getAttribute("aria-label"))!.replace(/^Pin /, "").replace(/ to the card$/, "");
  await pin.click();
  await expect(drawer.getByRole("button", { name: `Unpin ${name} from the card` })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(lampCard.getByTestId("pinned")).toContainText(name);

  // Saved in the service, so another person's browser shows it too.
  const viewer = await browser.newPage();
  await open(viewer, OWNER, "/devices");
  await expect(viewer.getByTestId("pinned").filter({ hasText: name })).toBeVisible();
  await viewer.close();

  await page.getByRole("button", { name: "Details of Desk lamp" }).click();
  await drawer.getByRole("button", { name: `Unpin ${name} from the card` }).click();
  await page.keyboard.press("Escape");
  await expect(lampCard.getByTestId("pinned")).toHaveCount(0);
});

test("an administrator renames a device and moves it to a new room, and both stay after a reload", async ({ page }) => {
  await page.getByRole("button", { name: "Details of Desk lamp" }).click();
  const drawer = page.getByRole("dialog", { name: "Desk lamp" });
  await drawer.getByRole("button", { name: "Edit" }).click();
  await drawer.getByLabel("Name").fill("Reading lamp");
  await chooseOption(page, drawer.getByRole("combobox", { name: "Room" }), "A new room…");
  await drawer.getByLabel("New room's name").fill("Study");
  await drawer.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("dialog", { name: "Reading lamp" }).getByText("Desk lamp")).toBeVisible(); // the integration's name, kept
  await page.keyboard.press("Escape");
  await page.reload();
  await expect(section(page, "Study").getByRole("button", { name: "Details of Reading lamp" })).toBeVisible();
  await expect(section(page, "Living room").getByRole("button", { name: "Details of Reading lamp" })).toHaveCount(0);

  // An empty name goes back to the integration's.
  await page.getByRole("button", { name: "Details of Reading lamp" }).click();
  const renamed = page.getByRole("dialog", { name: "Reading lamp" });
  await renamed.getByRole("button", { name: "Edit" }).click();
  await renamed.getByLabel("Name").fill("");
  await chooseOption(page, renamed.getByRole("combobox", { name: "Room" }), "Living room");
  await renamed.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("dialog", { name: "Desk lamp" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(section(page, "Living room").getByRole("button", { name: "Details of Desk lamp" })).toBeVisible();
});

test("the Rooms page adds, renames, reorders and deletes rooms, and moves devices between them", async ({ page }) => {
  await page.goto("/rooms");
  await expect(page.getByRole("heading", { level: 1, name: "Rooms" })).toBeVisible();
  await page.getByLabel("New room's name").fill("Study");
  await page.getByRole("button", { name: "Add room" }).click();
  const study = page.getByTestId("room-Study");
  await expect(study.getByText("No devices yet.")).toBeVisible();

  await study.getByRole("button", { name: "Choose devices" }).click();
  const choose = page.getByRole("dialog", { name: "Devices in Study" });
  await choose.getByRole("checkbox", { name: /Desk lamp/ }).click(); // ticked once the service has moved it
  await expect(choose.getByRole("checkbox", { name: /Desk lamp/ })).toBeChecked();
  await page.keyboard.press("Escape");
  await expect(study.getByRole("list", { name: "Devices in Study" })).toContainText("Desk lamp");

  // Drag Study onto Bedroom's place.
  const from = await study.getByText("Desk lamp").boundingBox();
  const to = await page.getByTestId("room-Bedroom").boundingBox();
  await page.mouse.move(from!.x + from!.width / 2, from!.y + from!.height / 2);
  await page.mouse.down();
  await page.mouse.move(to!.x + to!.width / 2, to!.y + to!.height / 2, { steps: 8 });
  await page.mouse.up();
  await expect(page.getByTestId(/^room-/)).toHaveCount(3);
  await expect.poll(async () => (await page.getByTestId(/^room-/).evaluateAll((els) => els.map((e) => e.getAttribute("data-testid"))))).toEqual(["room-Living room", "room-Study", "room-Bedroom"]);
  await study.getByRole("button", { name: "Rename" }).click();
  await study.getByLabel("New name for Study").fill("Office");
  await study.getByRole("button", { name: "Save" }).click();
  const office = page.getByTestId("room-Office");
  await expect(office.getByRole("list", { name: "Devices in Office" })).toContainText("Desk lamp");

  await page.getByRole("link", { name: "Devices", exact: true }).click();
  const jump = page.getByRole("navigation", { name: "Jump to" }).getByRole("link");
  await expect(jump).toHaveText(["Living room", "Office", "Bedroom", "Other"]);
  await expect(section(page, "Office").getByRole("button", { name: "Details of Desk lamp" })).toBeVisible();

  await page.getByRole("link", { name: "Rooms", exact: true }).click();
  await page.getByTestId("room-Office").getByRole("button", { name: "Delete" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Delete" }).click();
  await expect(page.getByTestId("room-Office")).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Not in a room" })).toContainText("Desk lamp");
});
