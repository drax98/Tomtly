import path from "node:path";
import { defineConfig } from "vitest/config";

// The extension tests run in plain Node with mocked host APIs, so they don't use the web app's jsdom setup.
const repo = path.resolve(import.meta.dirname, "..");

export default defineConfig({
  root: repo,
  test: {
    environment: "node",
    include: ["extensions/test/**/*.test.ts"],
    coverage: {
      provider: "v8",
      include: ["extensions/*/**/*.ts"],
      exclude: ["extensions/test/**", "**/*.d.ts"],
      reporter: ["text", "json-summary"],
      reportsDirectory: path.resolve(import.meta.dirname, "node_modules/.cache/extensions-coverage"),
    },
  },
});
