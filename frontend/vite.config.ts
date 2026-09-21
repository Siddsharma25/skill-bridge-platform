// defineConfig from "vitest/config" (not plain "vite") — it re-exports
// Vite's own defineConfig with the `test` field's types merged in, so
// this one file configures both the dev/build tool and the test runner
// without a second config file drifting out of sync with plugins/aliases.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import tsconfigPaths from "vite-tsconfig-paths";
import { sentryVitePlugin } from "@sentry/vite-plugin";

// Sentry source map upload: opt-in, gated on SENTRY_AUTH_TOKEN (+
// SENTRY_ORG/SENTRY_PROJECT) all being set at *build* time — distinct from
// VITE_SENTRY_DSN, which is a runtime value baked into the client bundle.
// An auth token must never be a VITE_-prefixed var (those ship to the
// browser); it's read here, in Vite's Node-side config, and never
// reaches the client bundle. Omitting the plugin entirely when unset
// (rather than passing an empty authToken) keeps a normal `npm run build`
// silent and side-effect-free with no Sentry account configured — same
// degrade-gracefully convention as every optional integration in this
// repo. See docs/DEPLOYMENT.md.
const sentryBuildConfigured =
  process.env.SENTRY_AUTH_TOKEN && process.env.SENTRY_ORG && process.env.SENTRY_PROJECT;

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    tsconfigPaths(),
    ...(sentryBuildConfigured
      ? [
          sentryVitePlugin({
            org: process.env.SENTRY_ORG,
            project: process.env.SENTRY_PROJECT,
            authToken: process.env.SENTRY_AUTH_TOKEN,
            sourcemaps: {
              // Uploaded to Sentry for readable stack traces, then removed
              // from the actual build output — see the `sourcemap: "hidden"`
              // build option below for why they're generated at all.
              filesToDeleteAfterUpload: ["**/*.map"],
            },
          }),
        ]
      : []),
  ],
  build: {
    // "hidden": generate .map files (so the plugin above has something to
    // upload) without a `//# sourceMappingURL=` comment in the shipped JS —
    // a real browser never fetches/exposes them; only Sentry's own upload
    // reads them, and filesToDeleteAfterUpload removes them from dist/
    // afterward. Only turned on when the plugin actually runs — with no
    // Sentry account configured, there's no upload step to clean them up
    // afterward, so generating them at all would just ship dead weight to
    // Vercel for nothing.
    sourcemap: sentryBuildConfigured ? "hidden" : false,
  },
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