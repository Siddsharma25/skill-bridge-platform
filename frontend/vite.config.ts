// defineConfig from "vitest/config" (not plain "vite") — it re-exports
// Vite's own defineConfig with the `test` field's types merged in, so
// this one file configures both the dev/build tool and the test runner
// without a second config file drifting out of sync with plugins/aliases.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    tsconfigPaths(),
  ],
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    // No `globals: true` — every test file imports describe/it/expect
    // explicitly from "vitest", matching this codebase's existing
    // no-ambient-magic style (nothing else here relies on auto-injected
    // globals either).
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        "src/components/ui/**", // shadcn-generated primitives, not hand-written logic
        "src/**/*.d.ts",
        "src/main.tsx", // wiring/bootstrap, same "don't test entrypoints" convention the backend follows
      ],
    },
  },
});