/// <reference types="vitest/config" />
import path from "node:path";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type Plugin } from "vite";
import { mockApi } from "./dev/mockApi";

// Scripts are only TypeScript, but Monaco's entry registers some 80 languages and the CSS, HTML and JSON services with
// their workers. Each of those registrations becomes an empty module, so none of it is built or shipped.
const OTHER_LANGUAGES = /languages\/(definitions\/(?!typescript\/)[^/]+|features\/(css|html|json))\/register\.js$/;
const empty = "\0monaco-language-left-out";
const monacoTypeScriptOnly: Plugin = {
  name: "monaco-typescript-only",
  enforce: "pre",
  resolveId: (source, importer) => (importer?.includes("monaco-editor") && OTHER_LANGUAGES.test(source) ? empty : null),
  load: (id) => (id === empty ? "export {}" : null),
};

export default defineConfig({
  plugins: [react(), tailwindcss(), monacoTypeScriptOnly, ...(process.env.MOCK ? [mockApi()] : [])],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  server: { proxy: { "/api": "http://127.0.0.1:8099" } },
  // Monaco is one big lazy chunk that only advanced mode loads, so its size isn't worth a warning.
  build: { chunkSizeWarningLimit: 8000 },
  test: { environment: "jsdom", setupFiles: ["./src/test/setup.ts"], include: ["src/**/*.test.{ts,tsx}"] },
});
