import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The browser checks in this suite run against jsdom rather than a real engine.
// That is a deliberate trade: they run in CI on every change, where driving a
// real browser does not, and the behaviour they cover — how a value is parsed,
// which branch of a component renders — does not depend on a real layout.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    restoreMocks: true,
  },
});
