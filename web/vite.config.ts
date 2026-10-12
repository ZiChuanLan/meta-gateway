import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/console",
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          const path = id.replaceAll(String.fromCharCode(92), "/");
          if (
            ["react", "react-dom", "scheduler", "react-router", "react-router-dom"].some((name) =>
              path.includes(`/node_modules/${name}/`),
            )
          )
            return "framework";
          if (path.endsWith("/src/i18n/zh.ts") || path.endsWith("/src/i18n/en.ts"))
            return "locales";
        },
      },
    },
  },
  server: {
    port: 4173,
    proxy: {
      "/admin/": "http://127.0.0.1:4100",
      "/healthz": "http://127.0.0.1:4100",
      "/readyz": "http://127.0.0.1:4100",
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
    // Keep jsdom processes bounded on many-core hosts; preserve test deadlines.
    maxWorkers: 4,
    // React's development build exports `act`; the production build does not.
    // The shell sets NODE_ENV=production, which breaks @testing-library/react
    // (it falls back to the deprecated react-dom/test-utils entry and throws).
    env: { NODE_ENV: "development" },
  },
});
