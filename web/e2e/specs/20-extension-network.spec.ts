import { createSocket } from "node:dgram";
import { expect, test, type Page } from "@playwright/test";
import { card, confirmDialog, open, OWNER, ownerApi } from "../support/home";
import { freshHome } from "../support/stack";

test.beforeEach(async () => {
  await freshHome({ config: "network" });
  await ownerApi();
});

const pluginsPage = (page: Page) => open(page, OWNER, "/plugins");

/** The Playwright runner shares the service's network namespace, so loopback reaches what the extension listens on. */
function sendDatagram(port: number, text: string) {
  return new Promise<void>((resolve, reject) => {
    const socket = createSocket("udp4");
    socket.send(text, port, "127.0.0.1", (err) => {
      socket.close();
      if (err) reject(err);
      else resolve();
    });
  });
}

test("a broadcast the extension was granted is allowed, and recorded where it has connected", async ({ page }) => {
  await pluginsPage(page);
  const finder = card(page, "LAN Discover");
  await finder.getByRole("button", { name: "Approve access" }).click();
  await expect(confirmDialog(page)).toContainText("Any device on your local network");
  await confirmDialog(page).getByRole("button", { name: "Approve and start" }).click();

  await expect(finder.getByText("Running", { exact: true })).toBeVisible();
  await expect(finder.getByText("Where it has connected (1)")).toBeVisible();
  await finder.getByText("Where it has connected (1)").click();
  await expect(finder.getByText("udp broadcast port 9999")).toBeVisible();
  await expect(finder.getByText(/Blocked \d+ attempts?/)).toHaveCount(0);
});

test("a broadcast without the local network in its access is refused, and shown", async ({ page }) => {
  await pluginsPage(page);
  const quiet = card(page, "No LAN Discover");
  await quiet.getByRole("button", { name: "Approve access" }).click();
  await confirmDialog(page).getByRole("button", { name: "Approve and start" }).click();

  await expect(quiet.getByText("Blocked 1 attempt", { exact: true })).toBeVisible();
  await expect(quiet.getByText("It tried to do something it wasn't allowed to.")).toBeVisible();
  await expect(quiet.getByText('lan: discovery needs "lan" in access')).toBeVisible();
  await expect(quiet.getByText("udp broadcast port 9999")).toHaveCount(0);
});

test("a port it was granted can be listened on, and what arrives from outside is recorded", async ({ page }) => {
  await pluginsPage(page);
  const listener = card(page, "Port Listener");
  await listener.getByRole("button", { name: "Approve access" }).click();
  await expect(confirmDialog(page)).toContainText("Accept connections from outside on port 41001");
  await confirmDialog(page).getByRole("button", { name: "Approve and start" }).click();
  await expect(listener.getByText("Running", { exact: true })).toBeVisible();
  await listener.getByText(/Where it has connected/).click();
  await expect(listener.getByText("udp listen port 41001")).toBeVisible();

  // The listener is up once it has read a datagram, so keep sending until one gets through.
  await expect(async () => {
    await sendDatagram(41001, "hello");
    await expect(listener.getByText("udp from outside 127.0.0.1")).toBeVisible({ timeout: 1_000 });
  }).toPass({ timeout: 30_000 });
  await expect(listener.getByText(/Blocked \d+ attempts?/)).toHaveCount(0);
});

test("listening on a port it didn't declare is refused, and shown", async ({ page }) => {
  await pluginsPage(page);
  const rogue = card(page, "Rogue Listener");
  await rogue.getByRole("button", { name: "Approve access" }).click();
  await confirmDialog(page).getByRole("button", { name: "Approve and start" }).click();

  await expect(rogue.getByText("Blocked 1 attempt", { exact: true })).toBeVisible();
  await expect(rogue.getByText("listen: udp.listen on port 41010 needs that port in access.listen")).toBeVisible();
  await expect(rogue.getByText("udp listen port 41010")).toHaveCount(0);
});
