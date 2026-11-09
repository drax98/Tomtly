/// <reference types="vitest/config" />
import path from "node:path";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";
import { mockApi } from "./dev/mockApi";

export default defineConfig({
  plugins: [react(), tailwindcss(), ...(process.env.MOCK ? [mockApi()] : [])],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  server: { proxy: { "/api": "http://127.0.0.1:8099" } },
  // Monaco is one big lazy chunk that only advanced mode loads, so its size isn't worth a warning.
  build: { chunkSizeWarningLimit: 8000 },
  test: { environment: "jsdom", setupFiles: ["./src/test/setup.ts"], include: ["src/**/*.test.{ts,tsx}"] },
});
