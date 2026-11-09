import { defineConfig, devices } from "@playwright/test";

// One worker: every spec file shares one service and resets it in beforeAll. Docker Desktop also has a limit, so it's the
// honest choice as well as the simple one.
export default defineConfig({
  testDir: "./specs",
  workers: 1,
  fullyParallel: false,
  retries: 0,
  forbidOnly: true,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  outputDir: "./.results/artifacts",
  reporter: [["list"], ["html", { outputFolder: "./.results/report", open: "never" }], ["json", { outputFile: "./.results/results.json" }]],
  use: {
    baseURL: "http://localhost:8099",
    trace: "on",
    screenshot: "only-on-failure",
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    ...devices["Desktop Chrome"],
  },
});
